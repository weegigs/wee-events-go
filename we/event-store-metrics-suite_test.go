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
	assert.InDelta(t, 39.0128, summary.StdDevMS, 0.001)
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

func TestRunMeasuredWaveRecordsWaveAndOperationDurations(t *testing.T) {
	result := runMeasuredWave(3, func(worker int) error {
		time.Sleep(time.Duration(worker+1) * time.Millisecond)
		return nil
	})

	require.NoError(t, result.Err)
	assert.Len(t, result.OperationDurations, 3)
	for _, duration := range result.OperationDurations {
		assert.Greater(t, duration, time.Duration(0))
	}
	assert.GreaterOrEqual(t, result.WaveDuration, 3*time.Millisecond)
	assert.Equal(t, 3, result.Attempts)
	assert.Zero(t, result.Failures)
}

func TestRunMeasuredWaveReportsFailuresWithoutDroppingDurations(t *testing.T) {
	result := runMeasuredWave(2, func(worker int) error {
		time.Sleep(time.Millisecond)
		if worker == 1 {
			return assert.AnError
		}
		return nil
	})

	require.ErrorIs(t, result.Err, assert.AnError)
	assert.Len(t, result.OperationDurations, 2)
	for _, duration := range result.OperationDurations {
		assert.Greater(t, duration, time.Duration(0))
	}
	assert.Equal(t, 2, result.Attempts)
	assert.Equal(t, 1, result.Failures)
}

func TestReportMeasuredWorkloadReportsEventsFromWrittenEventsPerWave(t *testing.T) {
	result := measuredWorkloadResult{
		WaveDurations: []time.Duration{
			10 * time.Millisecond,
			10 * time.Millisecond,
		},
		OperationDurations: []time.Duration{
			time.Millisecond,
			time.Millisecond,
			time.Millisecond,
			time.Millisecond,
		},
		Attempts: 8,
	}

	benchmark := testing.Benchmark(func(b *testing.B) {
		reportMeasuredWorkload(b, result, 4, 2)
	})

	assert.InDelta(t, 400.0, benchmark.Extra["ops/s"], 0.001)
	assert.InDelta(t, 200.0, benchmark.Extra["events/s"], 0.001)
}

func TestRunMeasuredWorkloadUsesBenchmarkIterationsAndReportsCustomMetrics(t *testing.T) {
	suite := &EventStoreMetricsSuite{
		config: EventStoreMetricsConfig{Waves: 3},
	}
	var result measuredWorkloadResult

	benchmark := testing.Benchmark(func(b *testing.B) {
		result = suite.runMeasuredWorkload(b, 1, func(worker int) error {
			time.Sleep(100 * time.Microsecond)
			return nil
		})
		reportMeasuredWorkload(b, result, 1, 1)
	})

	assert.Greater(t, benchmark.NsPerOp(), int64(10_000))
	assert.Len(t, result.WaveDurations, suite.config.Waves)
	assert.Len(t, result.OperationDurations, suite.config.Waves)
	assert.Equal(t, suite.config.Waves, result.Attempts)
	assert.Contains(t, benchmark.Extra, "ops/s")
	assert.Contains(t, benchmark.Extra, "wave_p95_ms")
	assert.Contains(t, benchmark.Extra, "op_p95_ms")
}
