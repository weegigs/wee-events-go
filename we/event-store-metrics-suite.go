package we

import (
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type EventStoreMetricsConfig struct {
	Widths           []int
	Waves            int
	EventsPerPublish int
	SeedEvents       int
	ReadSeedEvents   int
}

func defaultEventStoreMetricsConfig() EventStoreMetricsConfig {
	return EventStoreMetricsConfig{
		Widths:           []int{1, 10, 100},
		Waves:            30,
		EventsPerPublish: 1,
		SeedEvents:       10,
		ReadSeedEvents:   50,
	}
}

func metricsConfigFromEnv() (EventStoreMetricsConfig, error) {
	cfg := defaultEventStoreMetricsConfig()
	if raw := strings.TrimSpace(os.Getenv("WE_METRICS_WIDTHS")); raw != "" {
		widths, err := ParsePositiveIntList(raw)
		if err != nil {
			return EventStoreMetricsConfig{}, fmt.Errorf("invalid WE_METRICS_WIDTHS: %w", err)
		}
		cfg.Widths = widths
	}
	if raw := strings.TrimSpace(os.Getenv("WE_METRICS_WAVES")); raw != "" {
		waves, err := strconv.Atoi(raw)
		if err != nil || waves <= 0 {
			return EventStoreMetricsConfig{}, fmt.Errorf("invalid WE_METRICS_WAVES %q", raw)
		}
		cfg.Waves = waves
	}
	return cfg, nil
}

// ParsePositiveIntList parses a comma-separated list of positive integers,
// trimming whitespace around each field. It returns an error naming the
// offending field if any value is missing, non-numeric, or not positive.
func ParsePositiveIntList(raw string) ([]int, error) {
	fields := strings.Split(raw, ",")
	values := make([]int, 0, len(fields))
	for _, field := range fields {
		trimmed := strings.TrimSpace(field)
		value, err := strconv.Atoi(trimmed)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("value %q is not a positive integer", field)
		}
		values = append(values, value)
	}
	return values, nil
}

type EventStoreMetricsSuite struct {
	ctx    context.Context
	store  EventStore
	config EventStoreMetricsConfig
}

func NewEventStoreMetricsSuite(ctx context.Context, store EventStore, config EventStoreMetricsConfig) *EventStoreMetricsSuite {
	return &EventStoreMetricsSuite{ctx: ctx, store: store, config: config}
}

func NewEventStoreMetricsSuiteFromEnv(ctx context.Context, store EventStore) (*EventStoreMetricsSuite, error) {
	cfg, err := metricsConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return NewEventStoreMetricsSuite(ctx, store, cfg), nil
}

// RunMetricsBenchmark builds a metrics suite over store from environment
// configuration (see NewEventStoreMetricsSuiteFromEnv) and runs it. Every
// backend's BenchmarkMetrics* entry point shares this body.
func RunMetricsBenchmark(b *testing.B, ctx context.Context, store EventStore) {
	b.Helper()
	suite, err := NewEventStoreMetricsSuiteFromEnv(ctx, store)
	if err != nil {
		b.Fatal(err)
	}
	suite.Run(b)
}

func (s *EventStoreMetricsSuite) Run(b *testing.B) {
	for _, width := range s.config.Widths {
		b.Run(fmt.Sprintf("write_fanout/spread/%d", width), s.writeFanout(width, makeSpreadId))
		b.Run(fmt.Sprintf("write_fanout/concentrated/%d", width), s.writeFanout(width, concentratedId))
		b.Run(fmt.Sprintf("read_fanout/spread/%d", width), s.readFanout(width, makeSpreadId))
		b.Run(fmt.Sprintf("read_fanout/concentrated/%d", width), s.readFanout(width, concentratedId))
		b.Run(fmt.Sprintf("mixed/read_write/%d", width), s.mixedFanout(width))
	}
}

func (s *EventStoreMetricsSuite) writeFanout(width int, idFor func(worker int) AggregateId) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		ids := make([]AggregateId, width)
		for worker := range width {
			ids[worker] = idFor(worker)
			seedAggregate(b, s.ctx, s.store, ids[worker], s.config.SeedEvents)
		}
		events := makeBenchmarkEvents(s.config.EventsPerPublish)
		result := s.runMeasuredWorkload(b, width, func(worker int) error {
			return s.store.Publish(s.ctx, ids[worker], Options(), events...)
		})
		reportMeasuredWorkload(b, result, width, width*s.config.EventsPerPublish)
	}
}

func (s *EventStoreMetricsSuite) readFanout(width int, idFor func(worker int) AggregateId) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		ids := make([]AggregateId, width)
		for worker := range width {
			ids[worker] = idFor(worker)
			seedAggregate(b, s.ctx, s.store, ids[worker], s.config.ReadSeedEvents)
		}
		result := s.runMeasuredWorkload(b, width, func(worker int) error {
			_, err := s.store.Load(s.ctx, ids[worker])
			return err
		})
		reportMeasuredWorkload(b, result, width, 0)
	}
}

func (s *EventStoreMetricsSuite) mixedFanout(width int) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		workers := 2 * width
		ids := make([]AggregateId, workers)
		for worker := range workers {
			ids[worker] = makeSpreadId(worker)
			seedAggregate(b, s.ctx, s.store, ids[worker], s.config.ReadSeedEvents)
		}
		events := makeBenchmarkEvents(s.config.EventsPerPublish)
		result := s.runMeasuredWorkload(b, workers, func(worker int) error {
			if worker%2 == 0 {
				_, err := s.store.Load(s.ctx, ids[worker])
				return err
			}
			return s.store.Publish(s.ctx, ids[worker], Options(), events...)
		})
		reportMeasuredWorkload(b, result, workers, width*s.config.EventsPerPublish)
	}
}

type durationSummary struct {
	Count    int
	AvgMS    float64
	P50MS    float64
	P95MS    float64
	MaxMS    float64
	StdDevMS float64
}

func summarizeDurations(values []time.Duration) durationSummary {
	if len(values) == 0 {
		return durationSummary{}
	}

	sorted := append([]time.Duration(nil), values...)
	slices.Sort(sorted)

	var sum float64
	for _, value := range sorted {
		sum += durationMS(value)
	}
	avg := sum / float64(len(sorted))

	var variance float64
	for _, value := range sorted {
		delta := durationMS(value) - avg
		variance += delta * delta
	}
	variance /= float64(len(sorted))

	return durationSummary{
		Count:    len(sorted),
		AvgMS:    avg,
		P50MS:    durationMS(percentileDuration(sorted, 50)),
		P95MS:    durationMS(percentileDuration(sorted, 95)),
		MaxMS:    durationMS(sorted[len(sorted)-1]),
		StdDevMS: math.Sqrt(variance),
	}
}

func percentileDuration(sorted []time.Duration, percentile float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := int(math.Ceil((percentile / 100) * float64(len(sorted))))
	index := max(rank-1, 0)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func durationMS(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

type measuredWaveResult struct {
	WaveDuration       time.Duration
	OperationDurations []time.Duration
	Attempts           int
	Failures           int
	Err                error
}

// RunMeasuredWave runs one wave of exactly width goroutines, each performing
// one operation, and joins them. It records each operation's duration and
// count of attempts/failures alongside the joined error. Worker errors are
// captured atomically and raised after the join because b.Fatal must not be
// called from worker goroutines — FailNow is only valid on the benchmark
// goroutine.
func RunMeasuredWave(width int, op func(worker int) error) measuredWaveResult {
	var wg sync.WaitGroup
	var firstErr atomic.Pointer[error]
	var failures atomic.Int64
	durations := make([]time.Duration, width)
	start := time.Now()

	wg.Add(width)
	for worker := range width {
		go func() {
			defer wg.Done()
			opStart := time.Now()
			err := op(worker)
			durations[worker] = time.Since(opStart)
			if err != nil {
				failures.Add(1)
				firstErr.CompareAndSwap(nil, &err)
			}
		}()
	}
	wg.Wait()

	var err error
	if p := firstErr.Load(); p != nil {
		err = *p
	}
	return measuredWaveResult{
		WaveDuration:       time.Since(start),
		OperationDurations: durations,
		Attempts:           width,
		Failures:           int(failures.Load()),
		Err:                err,
	}
}

type measuredWorkloadResult struct {
	WaveDurations      []time.Duration
	OperationDurations []time.Duration
	Attempts           int
	Failures           int
}

func (s *EventStoreMetricsSuite) runMeasuredWorkload(b *testing.B, width int, op func(worker int) error) measuredWorkloadResult {
	b.Helper()
	if s.config.Waves <= 0 {
		b.Fatalf("metrics waves must be positive: %d", s.config.Waves)
	}

	var sample measuredWorkloadResult
	for b.Loop() {
		result := s.runFixedMeasuredWorkload(b, width, op)
		if sample.WaveDurations == nil {
			sample = result
		}
	}

	return sample
}

func (s *EventStoreMetricsSuite) runFixedMeasuredWorkload(b *testing.B, width int, op func(worker int) error) measuredWorkloadResult {
	b.Helper()
	result := measuredWorkloadResult{
		WaveDurations:      make([]time.Duration, 0, s.config.Waves),
		OperationDurations: make([]time.Duration, 0, s.config.Waves*width),
	}

	for wave := 0; wave < s.config.Waves; wave++ {
		waveResult := RunMeasuredWave(width, op)
		result.WaveDurations = append(result.WaveDurations, waveResult.WaveDuration)
		result.OperationDurations = append(result.OperationDurations, waveResult.OperationDurations...)
		result.Attempts += waveResult.Attempts
		result.Failures += waveResult.Failures
		if waveResult.Err != nil {
			b.Fatalf("metrics wave %d failed: %v", wave, waveResult.Err)
		}
	}

	return result
}

func reportMeasuredWorkload(b *testing.B, result measuredWorkloadResult, width int, eventsPerWave int) {
	b.Helper()
	waveSummary := summarizeDurations(result.WaveDurations)
	opSummary := summarizeDurations(result.OperationDurations)
	var totalWaveDuration time.Duration
	for _, duration := range result.WaveDurations {
		totalWaveDuration += duration
	}

	seconds := totalWaveDuration.Seconds()
	if seconds > 0 {
		b.ReportMetric(float64(result.Attempts)/seconds, "ops/s")
		if eventsPerWave > 0 {
			b.ReportMetric(float64(len(result.WaveDurations)*eventsPerWave)/seconds, "events/s")
		}
		b.ReportMetric(float64(len(result.WaveDurations))/seconds, "waves/s")
	}
	if result.Attempts > 0 {
		b.ReportMetric(float64(result.Failures)/float64(result.Attempts), "errors/op")
	}

	b.ReportMetric(float64(width), "workers")
	reportDurationSummary(b, "wave", waveSummary)
	reportDurationSummary(b, "op", opSummary)
}

func reportDurationSummary(b *testing.B, prefix string, summary durationSummary) {
	b.Helper()
	b.ReportMetric(summary.AvgMS, prefix+"_avg_ms")
	b.ReportMetric(summary.P50MS, prefix+"_p50_ms")
	b.ReportMetric(summary.P95MS, prefix+"_p95_ms")
	b.ReportMetric(summary.MaxMS, prefix+"_max_ms")
	b.ReportMetric(summary.StdDevMS, prefix+"_stddev_ms")
}

// seedAggregate pre-populates an aggregate with count events outside any
// timed region. It must only be called from the benchmark goroutine: it
// fails via b.Fatal. Shared by both the benchmark and metrics suites.
func seedAggregate(b *testing.B, ctx context.Context, store EventStore, id AggregateId, count int) {
	b.Helper()
	if count == 0 {
		return
	}
	if err := store.Publish(ctx, id, Options(), makeBenchmarkEvents(count)...); err != nil {
		b.Fatal(err)
	}
}
