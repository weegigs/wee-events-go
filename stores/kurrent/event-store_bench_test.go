package kurrent

import (
	"context"
	"testing"

	"github.com/weegigs/wee-events-go/we"
)

// BenchmarkKurrent wires the shared benchmark suite to a disposable KurrentDB
// testcontainer. Requires Docker.
//
// PageSize is intentionally omitted so the store uses its default page size
// (97). The conformance tests pass PageSize(5) to exercise pagination
// boundary conditions, but benchmarks must measure the store under realistic
// read-path behaviour — pinning pagination to 5 events per request would
// dominate the load_scaling numbers and obscure true store throughput.
func BenchmarkKurrent(b *testing.B) {
	ctx := context.Background()
	store, cleanup := newBenchmarkStore(b, ctx)
	b.Cleanup(cleanup)

	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

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

func newBenchmarkStore(b *testing.B, ctx context.Context) (we.EventStore, func()) {
	b.Helper()
	store, cleanup, err := NewKurrentTestStore(ctx, we.MakeJSONEncoder())
	if err != nil {
		b.Fatal(err)
	}
	return store, cleanup
}
