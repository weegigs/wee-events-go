package ds

import (
	"context"
	"testing"

	"github.com/weegigs/wee-events-go/we"
)

// BenchmarkDynamo wires the shared benchmark suite to a disposable
// dynamodb-local testcontainer. Requires Docker.
func BenchmarkDynamo(b *testing.B) {
	ctx := context.Background()
	store, cleanup := newBenchmarkStore(b, ctx)
	b.Cleanup(cleanup)

	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

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

func newBenchmarkStore(b *testing.B, ctx context.Context) (we.EventStore, func()) {
	b.Helper()
	store, cleanup, err := DynamoTestStore(ctx)
	if err != nil {
		b.Fatal(err)
	}
	return store, cleanup
}
