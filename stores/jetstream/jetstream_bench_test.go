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

	store, cleanup, err := jetstream.NewTestStore(ctx)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(cleanup)

	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}
