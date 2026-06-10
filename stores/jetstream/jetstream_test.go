package jetstream_test

import (
	"context"
	"testing"

	"github.com/weegigs/wee-events-go/stores/jetstream"
	"github.com/weegigs/wee-events-go/we"
)

func TestEventStore(t *testing.T) {
	ctx := context.Background()
	store, cleanup, err := jetstream.NewTestStore(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	t.Run("jetstream event store validation", func(t *testing.T) {
		suite := we.NewEventStoreValidationSuite(ctx, store)
		suite.Run(t)
	})
}
