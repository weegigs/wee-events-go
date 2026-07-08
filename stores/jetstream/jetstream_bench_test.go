// Package jetstream_test contains benchmarks for the JetStream event store.
// BenchmarkJetStream requires Docker — testcontainers provisions a NATS
// container automatically on first call.
package jetstream_test

import (
	"context"
	"testing"

	"github.com/weegigs/wee-events-go/stores/jetstream"
	"github.com/weegigs/wee-events-go/we"
)

func BenchmarkJetStream(b *testing.B) {
	ctx := context.Background()
	store, cleanup := newBenchmarkStore(b, ctx)
	b.Cleanup(cleanup)

	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

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

func newBenchmarkStore(b *testing.B, ctx context.Context) (we.EventStore, func()) {
	b.Helper()
	store, cleanup, err := jetstream.NewTestStore(ctx)
	if err != nil {
		b.Fatal(err)
	}
	return store, cleanup
}
