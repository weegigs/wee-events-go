package we

import (
	"context"
	"testing"
)

// BenchmarkMemory runs the shared event-store benchmark suite against the
// in-memory reference store. It compiles under `just test` (keeping the suite
// build-checked) but only executes under `go test -bench` — no Docker needed.
func BenchmarkMemory(b *testing.B) {
	suite := NewEventStoreBenchmarkSuite(context.Background(), newMemoryEventStore(MakeJSONEncoder()))
	suite.Run(b)
}

func BenchmarkMemoryMetrics(b *testing.B) {
	ctx := context.Background()
	suite, err := NewEventStoreMetricsSuiteFromEnv(ctx, newMemoryEventStore(MakeJSONEncoder()))
	if err != nil {
		b.Fatal(err)
	}
	suite.Run(b)
}
