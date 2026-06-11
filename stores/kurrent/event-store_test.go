package kurrent

import (
	"context"
	"fmt"
	"testing"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/weegigs/wee-events-go/we"
)

type TestEvent struct {
	Value string `json:"value"`
}

func TestEventStore(t *testing.T) {
	ctx := context.Background()
	store, cleanup, err := NewKurrentTestStore(ctx, we.MakeJSONEncoder(), PageSize(5))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	t.Run("kurrentdb event store validation", func(t *testing.T) {
		suite := we.NewEventStoreValidationSuite(ctx, store)
		suite.Run(t)
	})

	t.Run("kurrentdb shared-backing validation", func(t *testing.T) {
		// A second instance over the same KurrentDB server (the store owns its
		// client, so the second store gets its own client from the same
		// settings) shares one backing.
		second, err := NewEventStore(store.settings, we.MakeJSONEncoder(), PageSize(5))
		require.NoError(t, err)
		defer func() { require.NoError(t, second.Close()) }()
		suite := we.NewSharedBackingSuite(ctx, store, second)
		suite.Run(t)
	})

	t.Run("per-publish CBOR override round-trips", func(t *testing.T) {
		// ENCODING-S2.R3 — a CBOR override on a JSON store produces a
		// mixed-encoding stream that loads and decodes per event.
		id := we.AggregateId{Type: "test", Key: "cbor-override"}

		require.NoError(t, store.Publish(ctx, id, we.Options(), TestEvent{Value: "json"}))
		require.NoError(t, store.Publish(ctx, id, we.Options(we.WithEncoder(we.MakeCBOREncoder())), TestEvent{Value: "cbor"}))

		loaded, err := store.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, loaded.Events, 2)
		assert.Equal(t, we.JSONEncoding, loaded.Events[0].Data.Encoding)
		assert.Equal(t, we.CBOREncoding, loaded.Events[1].Data.Encoding)
		for i, want := range []string{"json", "cbor"} {
			var decoded TestEvent
			require.NoError(t, we.UnmarshalFromData(loaded.Events[i].Data, &decoded))
			assert.Equal(t, want, decoded.Value)
		}
	})

	t.Run("CBOR constructor encoder round-trips", func(t *testing.T) {
		// ENCODING-S2.R1 — a store constructed with the CBOR encoder publishes
		// envelopes carrying application/cbor with verbatim payload bytes.
		cborStore, err := NewEventStore(store.settings, we.MakeCBOREncoder(), PageSize(5))
		require.NoError(t, err)
		defer func() { require.NoError(t, cborStore.Close()) }()

		id := we.AggregateId{Type: "test", Key: "cbor-constructor"}
		event := TestEvent{Value: "cbor-constructor"}
		require.NoError(t, cborStore.Publish(ctx, id, we.Options(), event))

		loaded, err := cborStore.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, loaded.Events, 1)

		got := loaded.Events[0].Data
		assert.Equal(t, we.CBOREncoding, got.Encoding)

		// Verbatim bytes: the store persists exactly what the constructor
		// encoder produced (fxamacker/cbor's default mode encodes this struct
		// deterministically, so byte equality is sound).
		want, err := we.MakeCBOREncoder().Encode(event)
		require.NoError(t, err)
		assert.Equal(t, []byte(want.Data), []byte(got.Data))

		var decoded TestEvent
		require.NoError(t, we.UnmarshalFromData(got, &decoded))
		assert.Equal(t, event, decoded)
	})

	t.Run("nil encoder override rejected at publish", func(t *testing.T) {
		// ENCODING-S2.R5 — an explicit nil override errors and records nothing.
		id := we.AggregateId{Type: "test", Key: "nil-encoder-override"}

		err := store.Publish(ctx, id, we.Options(we.WithEncoder(nil)), TestEvent{Value: "x"})
		require.Error(t, err)
		require.ErrorIs(t, err, we.NilEncoder)
		assert.Contains(t, err.Error(), "kurrent:")

		loaded, err := store.Load(ctx, id)
		require.NoError(t, err)
		assert.Empty(t, loaded.Events)
	})

	t.Run("should batch publish", func(t *testing.T) {
		var testId = we.AggregateId{Type: "test", Key: "should-batch-publish"}

		events := createEvents(10)

		err := store.Publish(ctx, testId, we.PublishOptions{}, events...)
		if !assert.Nil(t, err) {
			return
		}

		aggregate, err := store.Load(ctx, testId)
		if !assert.Nil(t, err) {
			return
		}

		assert.NotNil(t, aggregate)
		assert.Equal(t, 10, len(aggregate.Events))
		assert.Equal(t, we.Revision("0000000000000000000000000a"), aggregate.Revision)
	})

}

// Close is terminal: an operation after Close sees the closed client's
// ErrorCodeConnectionClosed and reaches the recovery path, which must refuse
// with StoreClosed rather than rebuild a client nobody would close. No server
// is needed — kurrentdb.NewClient does not dial eagerly, and the closed
// client refuses pre-dispatch.
func TestCloseIsTerminal(t *testing.T) {
	ctx := context.Background()

	settings, err := kurrentdb.ParseConnectionString("kurrentdb://localhost:2113?tls=false")
	require.NoError(t, err)

	store, err := NewEventStore(settings, we.MakeJSONEncoder())
	require.NoError(t, err)

	require.NoError(t, store.Close())
	require.NoError(t, store.Close(), "a second Close is a no-op state, not an error")

	id := we.AggregateId{Type: "test", Key: "closed-store"}

	err = store.Publish(ctx, id, we.Options(), TestEvent{Value: "x"})
	require.ErrorIs(t, err, StoreClosed, "publish after Close must fail with StoreClosed, not rebuild")

	_, err = store.Load(ctx, id)
	require.ErrorIs(t, err, StoreClosed, "load after Close must fail with StoreClosed, not rebuild")
}

// ENCODING-S2.R2 — a nil constructor encoder is an error, not a deferred panic.
func TestNilEncoderRejectedAtConstruction(t *testing.T) {
	store, err := NewEventStore(nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kurrent: encoder is required")
	assert.Nil(t, store)
}

func createEvents(count uint) []we.DomainEvent {
	events := make([]we.DomainEvent, count)
	for i := range events {
		events[i] = TestEvent{Value: fmt.Sprintf("test %d", i)}
	}
	return events
}
