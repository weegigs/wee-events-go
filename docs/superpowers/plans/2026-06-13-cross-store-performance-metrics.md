# Cross-Store Performance Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a cross-store performance metrics suite that reports throughput, average, p50, p95, max, and standard deviation for comparable event-store workloads across Memory, SQLite, sqld, Turso, JetStream, Kurrent, and DynamoDB.

**Architecture:** Keep the existing `EventStoreBenchmarkSuite` as the low-overhead Go benchmark and benchstat-compatible regression harness. Add a separate fixed-wave `EventStoreMetricsSuite` for workload-level reporting, so percentile collection does not distort the existing microbenchmark matrix. Store packages wire the metrics suite beside their current benchmarks using the same store constructors.

**Tech Stack:** Go `testing.B`, `b.ReportMetric`, existing `we.EventStore`, existing store benchmark constructors, `mise exec -- go test`, `just`.

---

## File Structure

- Create: `we/event-store-metrics-suite.go`
  - Owns the metrics-oriented benchmark suite, fixed workload config, duration summaries, percentile math, and custom `b.ReportMetric` output.
- Create: `we/event-store-metrics-suite_test.go`
  - Unit-tests summary math, percentile selection, standard deviation, and fan-out id partition semantics where possible.
- Modify: `we/event-store-benchmark-suite_test.go`
  - Adds `BenchmarkMemoryMetrics` using the in-memory store.
- Modify: `stores/sqlite/store_bench_test.go`
  - Adds metrics benchmarks for SQLite local strategies, sqld strategies, Turso global, and Turso fan-out where appropriate.
- Modify: `stores/jetstream/jetstream_bench_test.go`
  - Adds `BenchmarkMetricsJetStream`.
- Modify: `stores/kurrent/event-store_bench_test.go`
  - Adds `BenchmarkMetricsKurrent`.
- Modify: `stores/ds/event-store_bench_test.go`
  - Adds `BenchmarkMetricsDynamo`.
- Modify: `justfile`
  - Adds `bench-metrics`, `bench-metrics-integration`, `bench-metrics-turso`, and `bench-metrics-all` recipes.
- Modify: `documents/performance-benchmarks.md`
  - Documents the new metrics suite and how to interpret wave latency versus per-operation latency.
- Modify: `documents/sqlite-performance-state-2026-06-12.md`
  - References the new metrics commands and explains that future fan-out claims should use percentile and throughput output.

## Reporting Model

Each metrics sub-benchmark reports Go's normal `ns/op`, `B/op`, and `allocs/op`, plus custom metrics:

- `ops/s`: completed event-store operations per second.
- `events/s`: completed domain events per second.
- `wave_avg_ms`: average elapsed time for one parallel wave.
- `wave_p50_ms`: median wave latency.
- `wave_p95_ms`: p95 wave latency.
- `wave_max_ms`: maximum wave latency.
- `wave_stddev_ms`: standard deviation of wave latency.
- `op_avg_ms`: average individual operation latency inside waves.
- `op_p50_ms`: median individual operation latency.
- `op_p95_ms`: p95 individual operation latency.
- `op_max_ms`: maximum individual operation latency.
- `op_stddev_ms`: standard deviation of individual operation latency.
- `errors/op`: failed operations divided by attempted operations.

Canonical scale ladder:

- Local and Docker-backed stores: `1,10,100`.
- Turso: default to `1,10,100`; allow `WE_METRICS_WIDTHS=1,10,50` when account quota blocks 100 Turso databases.

## Task 1: Add Duration Summary Math

**Files:**
- Create: `we/event-store-metrics-suite.go`
- Create: `we/event-store-metrics-suite_test.go`

- [ ] **Step 1: Write failing tests for duration summaries**

Create `we/event-store-metrics-suite_test.go` with:

```go
package we

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeDurationsReportsAveragePercentilesMaxAndStdDev(t *testing.T) {
	summary := summarizeDurations([]time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		100 * time.Millisecond,
	})

	require.Equal(t, 5, summary.Count)
	assert.InDelta(t, 22.0, summary.AvgMS, 0.001)
	assert.InDelta(t, 3.0, summary.P50MS, 0.001)
	assert.InDelta(t, 100.0, summary.P95MS, 0.001)
	assert.InDelta(t, 100.0, summary.MaxMS, 0.001)
	assert.InDelta(t, 39.0256, summary.StdDevMS, 0.001)
}

func TestSummarizeDurationsHandlesEmptyInput(t *testing.T) {
	summary := summarizeDurations(nil)

	assert.Zero(t, summary.Count)
	assert.Zero(t, summary.AvgMS)
	assert.Zero(t, summary.P50MS)
	assert.Zero(t, summary.P95MS)
	assert.Zero(t, summary.MaxMS)
	assert.Zero(t, summary.StdDevMS)
}

func TestPercentileDurationUsesNearestRank(t *testing.T) {
	values := []time.Duration{
		1 * time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
		5 * time.Millisecond,
	}

	assert.Equal(t, 1*time.Millisecond, percentileDuration(values, 0))
	assert.Equal(t, 3*time.Millisecond, percentileDuration(values, 50))
	assert.Equal(t, 5*time.Millisecond, percentileDuration(values, 95))
	assert.Equal(t, 5*time.Millisecond, percentileDuration(values, 100))
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
mise exec -- go test ./we -run 'TestSummarizeDurations|TestPercentileDuration' -count=1
```

Expected: compile failure because `summarizeDurations` and `percentileDuration` do not exist.

- [ ] **Step 3: Implement summary math**

Create `we/event-store-metrics-suite.go` with:

```go
package we

import (
	"math"
	"slices"
	"time"
)

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
```

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
mise exec -- go test ./we -run 'TestSummarizeDurations|TestPercentileDuration' -count=1
```

Expected: `ok github.com/weegigs/wee-events-go/we`.

## Task 2: Add Fixed-Wave Metrics Suite

**Files:**
- Modify: `we/event-store-metrics-suite.go`
- Modify: `we/event-store-metrics-suite_test.go`

- [ ] **Step 1: Add tests for config defaults and environment parsing**

Append to `we/event-store-metrics-suite_test.go`:

```go
func TestMetricsConfigDefaultsUseOrderOfMagnitudeWidths(t *testing.T) {
	cfg := defaultEventStoreMetricsConfig()

	assert.Equal(t, []int{1, 10, 100}, cfg.Widths)
	assert.Equal(t, 30, cfg.Waves)
	assert.Equal(t, 1, cfg.EventsPerPublish)
	assert.Equal(t, 10, cfg.SeedEvents)
	assert.Equal(t, 50, cfg.ReadSeedEvents)
}

func TestMetricsConfigReadsWidthsFromEnvironment(t *testing.T) {
	t.Setenv("WE_METRICS_WIDTHS", "1,10,50")
	t.Setenv("WE_METRICS_WAVES", "7")

	cfg, err := metricsConfigFromEnv()

	require.NoError(t, err)
	assert.Equal(t, []int{1, 10, 50}, cfg.Widths)
	assert.Equal(t, 7, cfg.Waves)
}

func TestMetricsConfigRejectsInvalidWidths(t *testing.T) {
	t.Setenv("WE_METRICS_WIDTHS", "1,nope,10")

	_, err := metricsConfigFromEnv()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid WE_METRICS_WIDTHS")
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
mise exec -- go test ./we -run 'TestMetricsConfig' -count=1
```

Expected: compile failure because config functions do not exist.

- [ ] **Step 3: Implement config and metrics suite skeleton**

Extend `we/event-store-metrics-suite.go`:

```go
import (
	"context"
	"fmt"
	"os"
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
		widths, err := parsePositiveIntList(raw)
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

func parsePositiveIntList(raw string) ([]int, error) {
	fields := strings.Split(raw, ",")
	values := make([]int, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(strings.TrimSpace(field))
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
```

- [ ] **Step 4: Run config tests and verify they pass**

Run:

```bash
mise exec -- go test ./we -run 'TestMetricsConfig' -count=1
```

Expected: `ok github.com/weegigs/wee-events-go/we`.

## Task 3: Implement Measured Wave Runner

**Files:**
- Modify: `we/event-store-metrics-suite.go`
- Modify: `we/event-store-metrics-suite_test.go`

- [ ] **Step 1: Add measured wave tests**

Append to `we/event-store-metrics-suite_test.go`:

```go
func TestRunMeasuredWaveRecordsWaveAndOperationDurations(t *testing.T) {
	result := runMeasuredWave(3, func(worker int) error {
		time.Sleep(time.Duration(worker+1) * time.Millisecond)
		return nil
	})

	require.NoError(t, result.Err)
	assert.Len(t, result.OperationDurations, 3)
	assert.GreaterOrEqual(t, result.WaveDuration, 3*time.Millisecond)
	assert.Equal(t, 3, result.Attempts)
	assert.Zero(t, result.Failures)
}

func TestRunMeasuredWaveReportsFailuresWithoutDroppingDurations(t *testing.T) {
	result := runMeasuredWave(2, func(worker int) error {
		if worker == 1 {
			return assert.AnError
		}
		return nil
	})

	require.ErrorIs(t, result.Err, assert.AnError)
	assert.Len(t, result.OperationDurations, 2)
	assert.Equal(t, 2, result.Attempts)
	assert.Equal(t, 1, result.Failures)
}
```

- [ ] **Step 2: Run measured wave tests and verify they fail**

Run:

```bash
mise exec -- go test ./we -run 'TestRunMeasuredWave' -count=1
```

Expected: compile failure because `runMeasuredWave` does not exist.

- [ ] **Step 3: Implement measured wave runner**

Append to `we/event-store-metrics-suite.go`:

```go
type measuredWaveResult struct {
	WaveDuration       time.Duration
	OperationDurations []time.Duration
	Attempts           int
	Failures           int
	Err                error
}

func runMeasuredWave(width int, op func(worker int) error) measuredWaveResult {
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
```

- [ ] **Step 4: Run measured wave tests and verify they pass**

Run:

```bash
mise exec -- go test ./we -run 'TestRunMeasuredWave' -count=1
```

Expected: `ok github.com/weegigs/wee-events-go/we`.

## Task 4: Report Metrics From Fixed Workloads

**Files:**
- Modify: `we/event-store-metrics-suite.go`

- [ ] **Step 1: Implement suite `Run` and write-fanout workloads**

Append to `we/event-store-metrics-suite.go`:

```go
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
			seedMetricsAggregate(b, s.ctx, s.store, ids[worker], s.config.SeedEvents)
		}
		events := makeBenchmarkEvents(s.config.EventsPerPublish)
		result := s.runMeasuredWorkload(b, width, func(worker int) error {
			return s.store.Publish(s.ctx, ids[worker], Options(), events...)
		})
		reportMeasuredWorkload(b, result, width, s.config.EventsPerPublish)
	}
}

func (s *EventStoreMetricsSuite) readFanout(width int, idFor func(worker int) AggregateId) func(b *testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		ids := make([]AggregateId, width)
		for worker := range width {
			ids[worker] = idFor(worker)
			seedMetricsAggregate(b, s.ctx, s.store, ids[worker], s.config.ReadSeedEvents)
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
			seedMetricsAggregate(b, s.ctx, s.store, ids[worker], s.config.ReadSeedEvents)
		}
		events := makeBenchmarkEvents(s.config.EventsPerPublish)
		result := s.runMeasuredWorkload(b, workers, func(worker int) error {
			if worker%2 == 0 {
				_, err := s.store.Load(s.ctx, ids[worker])
				return err
			}
			return s.store.Publish(s.ctx, ids[worker], Options(), events...)
		})
		reportMeasuredWorkload(b, result, workers, s.config.EventsPerPublish)
	}
}
```

- [ ] **Step 2: Implement fixed-wave execution and reporting**

Append to `we/event-store-metrics-suite.go`:

```go
type measuredWorkloadResult struct {
	WaveDurations      []time.Duration
	OperationDurations []time.Duration
	Attempts           int
	Failures           int
}

func (s *EventStoreMetricsSuite) runMeasuredWorkload(b *testing.B, width int, op func(worker int) error) measuredWorkloadResult {
	b.Helper()
	waveDurations := make([]time.Duration, 0, s.config.Waves)
	operationDurations := make([]time.Duration, 0, s.config.Waves*width)
	var attempts int
	var failures int

	b.ResetTimer()
	for wave := 0; wave < s.config.Waves; wave++ {
		result := runMeasuredWave(width, op)
		waveDurations = append(waveDurations, result.WaveDuration)
		operationDurations = append(operationDurations, result.OperationDurations...)
		attempts += result.Attempts
		failures += result.Failures
		if result.Err != nil {
			b.Fatalf("metrics wave %d failed: %v", wave, result.Err)
		}
	}
	b.StopTimer()

	return measuredWorkloadResult{
		WaveDurations:      waveDurations,
		OperationDurations: operationDurations,
		Attempts:           attempts,
		Failures:           failures,
	}
}

func reportMeasuredWorkload(b *testing.B, result measuredWorkloadResult, width int, eventsPerPublish int) {
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
		if eventsPerPublish > 0 {
			b.ReportMetric(float64(result.Attempts*eventsPerPublish)/seconds, "events/s")
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

func seedMetricsAggregate(b *testing.B, ctx context.Context, store EventStore, id AggregateId, count int) {
	b.Helper()
	if count == 0 {
		return
	}
	if err := store.Publish(ctx, id, Options(), makeBenchmarkEvents(count)...); err != nil {
		b.Fatal(err)
	}
}
```

- [ ] **Step 3: Run all `we` tests**

Run:

```bash
mise exec -- go test ./we -count=1
```

Expected: `ok github.com/weegigs/wee-events-go/we`.

## Task 5: Wire Metrics Benchmarks For Memory And SQLite

**Files:**
- Modify: `we/event-store-benchmark-suite_test.go`
- Modify: `stores/sqlite/store_bench_test.go`

- [ ] **Step 1: Add helper to run metrics suite from store packages**

Append to `we/event-store-metrics-suite.go`:

```go
func NewEventStoreMetricsSuiteFromEnv(ctx context.Context, store EventStore) (*EventStoreMetricsSuite, error) {
	cfg, err := metricsConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return NewEventStoreMetricsSuite(ctx, store, cfg), nil
}
```

- [ ] **Step 2: Add memory metrics benchmark**

Modify `we/event-store-benchmark-suite_test.go`:

```go
func BenchmarkMemoryMetrics(b *testing.B) {
	ctx := context.Background()
	suite, err := NewEventStoreMetricsSuiteFromEnv(ctx, newMemoryEventStore(MakeJSONEncoder()))
	if err != nil {
		b.Fatal(err)
	}
	suite.Run(b)
}
```

- [ ] **Step 3: Add SQLite metrics helpers and local benchmarks**

Append to `stores/sqlite/store_bench_test.go`:

```go
func runSQLiteMetrics(b *testing.B, store we.EventStore) {
	b.Helper()
	ctx := context.Background()
	suite, err := we.NewEventStoreMetricsSuiteFromEnv(ctx, store)
	if err != nil {
		b.Fatal(err)
	}
	suite.Run(b)
}

func BenchmarkMetricsSqliteInMemory(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}

func BenchmarkMetricsSqliteLocalGlobal(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(filepath.Join(b.TempDir(), "events.db"), Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}

func BenchmarkMetricsSqliteLocalByType(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(b.TempDir(), ByType()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}
```

- [ ] **Step 4: Run memory and local SQLite metrics smoke tests**

Run:

```bash
WE_METRICS_WIDTHS=1,10 WE_METRICS_WAVES=3 mise exec -- go test -run '^$' -bench '^(BenchmarkMemoryMetrics|BenchmarkMetricsSqlite(InMemory|Local))$' -benchmem -benchtime=1x -timeout 10m ./we ./stores/sqlite
```

Expected: `PASS`, with custom metric columns including `ops/s`, `wave_p95_ms`, and `op_p95_ms`.

## Task 6: Wire Metrics Benchmarks For sqld And Turso

**Files:**
- Modify: `stores/sqlite/store_bench_test.go`

- [ ] **Step 1: Add sqld metrics benchmarks**

Append to `stores/sqlite/store_bench_test.go`:

```go
func BenchmarkMetricsSqliteSqldGlobal(b *testing.B) {
	ctx := context.Background()
	instance, err := ensureSqldBenchmarkInstance(ctx)
	if err != nil {
		b.Fatal(err)
	}
	store, err := NewStore(ctx, we.MakeJSONEncoder(), SqldNamespaced(instance.adminURL, instance.dataURL, "", Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}

func BenchmarkMetricsSqliteSqldByType(b *testing.B) {
	ctx := context.Background()
	instance, err := ensureSqldBenchmarkInstance(ctx)
	if err != nil {
		b.Fatal(err)
	}
	store, err := NewStore(ctx, we.MakeJSONEncoder(), SqldNamespaced(instance.adminURL, instance.dataURL, "", ByType()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}
```

- [ ] **Step 2: Add Turso metrics benchmarks**

Append to `stores/sqlite/store_bench_test.go`:

```go
func BenchmarkMetricsSqliteTursoGlobal(b *testing.B) {
	cfg := tursoConfigFromEnv(b)
	cfg.Prefix = cfg.Prefix + "-metrics-" + shortBenchmarkSuffix()
	b.Cleanup(func() { cleanupTursoBenchmarkPrefix(b, cfg) })

	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Turso(cfg, Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}

func BenchmarkMetricsSqliteTursoByType(b *testing.B) {
	cfg := tursoConfigFromEnv(b)
	cfg.Prefix = cfg.Prefix + "-metrics-" + shortBenchmarkSuffix()
	b.Cleanup(func() { cleanupTursoBenchmarkPrefix(b, cfg) })

	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Turso(cfg, ByType()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	runSQLiteMetrics(b, store)
}
```

- [ ] **Step 3: Run sqld metrics smoke test**

Run:

```bash
WE_METRICS_WIDTHS=1,10 WE_METRICS_WAVES=3 mise exec -- go test -run '^$' -bench '^BenchmarkMetricsSqliteSqld' -benchmem -benchtime=1x -timeout 30m ./stores/sqlite
```

Expected: `PASS`, with sqld metrics. Confirm that `BenchmarkMetricsSqliteSqldByType/write_fanout/spread/10` reports better `ops/s` than `concentrated/10`.

- [ ] **Step 4: Run Turso metrics smoke test with quota-safe widths**

Run:

```bash
WE_METRICS_WIDTHS=1,10 WE_METRICS_WAVES=3 mise exec -- go test -run '^$' -bench '^BenchmarkMetricsSqliteTurso(ByType|Global)$' -benchmem -benchtime=1x -timeout 120m ./stores/sqlite
```

Expected: `PASS`, or a Turso API quota/configuration error that clearly names the failing operation. If Turso quota blocks width 10, clean benchmark-prefixed databases and retry once.

## Task 7: Wire Metrics Benchmarks For JetStream, Kurrent, And DynamoDB

**Files:**
- Modify: `stores/jetstream/jetstream_bench_test.go`
- Modify: `stores/kurrent/event-store_bench_test.go`
- Modify: `stores/ds/event-store_bench_test.go`

- [ ] **Step 1: Add JetStream metrics benchmark**

Modify `stores/jetstream/jetstream_bench_test.go`:

```go
func BenchmarkMetricsJetStream(b *testing.B) {
	ctx := context.Background()
	store, cleanup := newBenchmarkStore(b, ctx)
	b.Cleanup(cleanup)
	suite, err := we.NewEventStoreMetricsSuiteFromEnv(ctx, store)
	if err != nil {
		b.Fatal(err)
	}
	suite.Run(b)
}
```

If the package does not currently expose `newBenchmarkStore`, first extract the existing `BenchmarkJetStream` setup into:

```go
func newBenchmarkStore(b *testing.B, ctx context.Context) (we.EventStore, func()) {
	b.Helper()
	// Move the existing container/store setup from BenchmarkJetStream here.
	// Return the constructed store and cleanup closure used by both benchmarks.
}
```

- [ ] **Step 2: Add Kurrent metrics benchmark**

Modify `stores/kurrent/event-store_bench_test.go`:

```go
func BenchmarkMetricsKurrent(b *testing.B) {
	ctx := context.Background()
	store, cleanup := newBenchmarkStore(b, ctx)
	b.Cleanup(cleanup)
	suite, err := we.NewEventStoreMetricsSuiteFromEnv(ctx, store)
	if err != nil {
		b.Fatal(err)
	}
	suite.Run(b)
}
```

If needed, extract the existing Kurrent benchmark setup into a `newBenchmarkStore` helper using the same pattern as JetStream.

- [ ] **Step 3: Add Dynamo metrics benchmark**

Modify `stores/ds/event-store_bench_test.go`:

```go
func BenchmarkMetricsDynamo(b *testing.B) {
	ctx := context.Background()
	store, cleanup := newBenchmarkStore(b, ctx)
	b.Cleanup(cleanup)
	suite, err := we.NewEventStoreMetricsSuiteFromEnv(ctx, store)
	if err != nil {
		b.Fatal(err)
	}
	suite.Run(b)
}
```

If needed, extract the existing Dynamo benchmark setup into a `newBenchmarkStore` helper using the same pattern as JetStream.

- [ ] **Step 4: Run Docker-backed metrics smoke test**

Run:

```bash
WE_METRICS_WIDTHS=1,10 WE_METRICS_WAVES=3 mise exec -- go test -p 1 -run '^$' -bench '^BenchmarkMetrics(JetStream|Kurrent|Dynamo)$' -benchmem -benchtime=1x -timeout 120m ./stores/jetstream ./stores/kurrent ./stores/ds
```

Expected: `PASS`, with metrics columns for each store.

## Task 8: Add Just Recipes For Metrics Runs

**Files:**
- Modify: `justfile`

- [ ] **Step 1: Add metrics recipes**

Modify `justfile`:

```just
# Run fixed-wave metrics benchmarks for local stores.
bench-metrics widths='1,10,100' waves='30' filter='^(BenchmarkMemoryMetrics|BenchmarkMetricsSqlite(InMemory|Local))':
    WE_METRICS_WIDTHS='{{widths}}' WE_METRICS_WAVES='{{waves}}' go test -run '^$' -bench '{{filter}}' -benchmem -benchtime=1x -timeout 60m ./we ./stores/sqlite

# Run fixed-wave metrics benchmarks for Docker-backed stores.
bench-metrics-integration widths='1,10,100' waves='30' filter='^(BenchmarkMetricsSqliteSqld|BenchmarkMetricsJetStream|BenchmarkMetricsKurrent|BenchmarkMetricsDynamo)':
    WE_METRICS_WIDTHS='{{widths}}' WE_METRICS_WAVES='{{waves}}' go test -p 1 -run '^$' -bench '{{filter}}' -benchmem -benchtime=1x -timeout 120m ./stores/sqlite ./stores/jetstream ./stores/kurrent ./stores/ds

# Run fixed-wave metrics benchmarks for live Turso.
bench-metrics-turso widths='1,10,100' waves='30' filter='^BenchmarkMetricsSqliteTurso': check-turso-env
    WE_METRICS_WIDTHS='{{widths}}' WE_METRICS_WAVES='{{waves}}' go test -run '^$' -bench '{{filter}}' -benchmem -benchtime=1x -timeout 120m ./stores/sqlite

# Run every fixed-wave metrics tier, including live Turso.
bench-metrics-all widths='1,10,100' waves='30':
    just check-turso-env
    just bench-metrics '{{widths}}' '{{waves}}'
    just bench-metrics-integration '{{widths}}' '{{waves}}'
    just bench-metrics-turso '{{widths}}' '{{waves}}'
```

- [ ] **Step 2: Verify recipe listing**

Run:

```bash
mise exec -- just --list | rg 'bench-metrics'
```

Expected: all four new recipes appear.

- [ ] **Step 3: Run local metrics recipe smoke test**

Run:

```bash
mise exec -- just bench-metrics 1,10 3
```

Expected: `PASS` for memory and local SQLite metrics.

## Task 9: Document Interpretation And Reporting Format

**Files:**
- Modify: `documents/performance-benchmarks.md`
- Modify: `documents/sqlite-performance-state-2026-06-12.md`

- [ ] **Step 1: Add metrics section to performance docs**

Add this section near the top of `documents/performance-benchmarks.md`:

```markdown
## Fixed-Wave Metrics Benchmarks

The standard benchmark suite remains the regression harness for `ns/op`,
allocations, and `benchstat` comparisons. The fixed-wave metrics suite is the
reporting harness for throughput and latency distribution.

Run local metrics:

```bash
mise exec -- just bench-metrics
```

Run Docker-backed metrics:

```bash
mise exec -- just bench-metrics-integration
```

Run live Turso metrics:

```bash
mise exec -- just bench-metrics-turso
```

Canonical widths are `1,10,100`. If live Turso quota prevents 100 provisioned
databases, run `mise exec -- just bench-metrics-turso 1,10,50` and label `50`
as a quota-capped fallback.

For wave workloads, `ns/op` is the elapsed time of the fixed workload. The
custom metrics provide the useful comparison:

- `ops/s` and `events/s` describe throughput.
- `wave_p95_ms` describes the tail of the whole parallel wave.
- `op_p95_ms` describes the tail of individual operations inside the wave.
- `errors/op` must be zero for normal fan-out scenarios.
```
```

- [ ] **Step 2: Update SQLite performance state doc**

In `documents/sqlite-performance-state-2026-06-12.md`, add:

```markdown
Future fan-out reporting should use the fixed-wave metrics suite instead of
single-run `ns/op` alone. The headline comparison should include `ops/s`,
`wave_p95_ms`, and `op_p95_ms` for `1,10,100` widths. Turso may use `1,10,50`
only when account quota blocks width 100.
```

- [ ] **Step 3: Verify docs render as plain Markdown**

Run:

```bash
mise exec -- go test ./we ./stores/sqlite -run '^$' -bench '^$'
```

Expected: packages compile; docs are plain Markdown and require no build step.

## Task 10: Full Verification

**Files:**
- All files touched above.

- [ ] **Step 1: Run unit tests**

Run:

```bash
mise exec -- go test ./we ./stores/sqlite -count=1
```

Expected: `PASS`.

- [ ] **Step 2: Run local metrics smoke**

Run:

```bash
mise exec -- just bench-metrics 1,10 3
```

Expected: `PASS`; output includes `ops/s`, `wave_p95_ms`, `op_p95_ms`.

- [ ] **Step 3: Run Docker-backed metrics smoke**

Run:

```bash
mise exec -- just bench-metrics-integration 1,10 3
```

Expected: `PASS`; no Testcontainers port race because recipe uses `go test -p 1`.

- [ ] **Step 4: Run Turso metrics smoke**

Run:

```bash
mise exec -- just bench-metrics-turso 1,10 3
```

Expected: `PASS` if Turso env and quota are available. If it fails with missing Turso env, report that `check-turso-env` correctly failed. If it fails with Turso quota, clean databases matching `TURSO_DB_PREFIX-*` and retry once.

- [ ] **Step 5: Run existing regression benchmarks that guard current behavior**

Run:

```bash
mise exec -- go test -run '^$' -bench '^BenchmarkShardFanoutSqld$' -benchmem -benchtime=1x -timeout 30m ./stores/sqlite
```

Expected: `PASS`; `many_shards/100` remains materially faster than `one_shard/100`.

- [ ] **Step 6: Check worktree diff**

Run:

```bash
git diff --stat
git diff -- we/event-store-metrics-suite.go we/event-store-metrics-suite_test.go justfile documents/performance-benchmarks.md documents/sqlite-performance-state-2026-06-12.md
```

Expected: diffs are limited to metrics suite, benchmark wiring, recipes, and docs.

## Self-Review

- Spec coverage: The plan adds throughput, average, p50, p95, max, standard deviation, and error-rate reporting across every existing store family.
- Placeholder scan: No `TBD`, `TODO`, or undefined future behavior remains. Where existing package setup helpers may need extraction, the exact helper signature and benchmark body are specified.
- Type consistency: `EventStoreMetricsSuite`, `EventStoreMetricsConfig`, `NewEventStoreMetricsSuiteFromEnv`, `runMeasuredWave`, and summary types are consistently named across tasks.
- Scope check: This is one focused subsystem: benchmark reporting. It intentionally does not change store semantics, retry behavior, or serialization format.
