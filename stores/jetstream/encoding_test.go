package jetstream

import (
	"context"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weegigs/wee-events-go/we"
)

type encodingTestEvent struct {
	Value string `json:"value"`
}

func makeEncodingAggregateId() we.AggregateId {
	return we.AggregateId{
		Type: "jetstream-encoding-test",
		Key:  ulid.Make().String(),
	}
}

// ENCODING-S2.R2 — a nil constructor encoder is an error, not a deferred panic.
// The guard fires before the connection is touched, so no broker is needed.
func TestNilEncoderRejectedAtConstruction(t *testing.T) {
	store, err := NewEventStore(context.Background(), "encoding", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jetstream: encoder is required")
	assert.Nil(t, store)
}

// TestEncoderResolution exercises ENCODING-S2.R3 and ENCODING-S2.R5 against a
// real JetStream broker: one container, one connection, a JSON-constructed and
// a CBOR-constructed store over distinct streams.
func TestEncoderResolution(t *testing.T) {
	ctx := context.Background()

	ctr, err := testcontainers.Run(
		ctx,
		"nats:alpine",
		testcontainers.WithExposedPorts("4222/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("4222/tcp")),
		testcontainers.WithCmd("--jetstream"),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := ctr.Terminate(ctx); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	port, err := ctr.MappedPort(ctx, "4222")
	require.NoError(t, err)

	nc, err := nats.Connect(fmt.Sprintf("nats://%s:%s", host, port.Port()))
	require.NoError(t, err)
	t.Cleanup(nc.Close)

	// Every store's stream binds the same change-set.> subjects, so one server
	// supports exactly one stream: both stores share the stream name (the same
	// idempotent CreateOrUpdateStream pattern the shared-backing suite uses)
	// and differ only in their constructor encoder.
	jsonStore, err := NewEventStore(ctx, "encoding", nc, we.MakeJSONEncoder())
	require.NoError(t, err)

	cborStore, err := NewEventStore(ctx, "encoding", nc, we.MakeCBOREncoder())
	require.NoError(t, err)

	// ENCODING-S2.R5 — an explicit nil override errors and records nothing.
	t.Run("nil encoder override is rejected and records nothing", func(t *testing.T) {
		id := makeEncodingAggregateId()

		err := jsonStore.Publish(ctx, id, we.Options(we.WithEncoder(nil)), encodingTestEvent{Value: "x"})
		require.Error(t, err)
		require.ErrorIs(t, err, we.NilEncoder)
		assert.Contains(t, err.Error(), "jetstream: encoder must not be nil")

		loaded, err := jsonStore.Load(ctx, id)
		require.NoError(t, err)
		assert.Empty(t, loaded.Events)
	})

	// ENCODING-S2.R3 — the per-publish override takes precedence over the
	// constructor encoder; with the envelope treating payload bytes as
	// opaque, a CBOR override on a JSON-constructed store round-trips
	// end-to-end.
	t.Run("cbor override round-trips on the json constructor store", func(t *testing.T) {
		id := makeEncodingAggregateId()

		require.NoError(t, jsonStore.Publish(ctx, id, we.Options(we.WithEncoder(we.MakeCBOREncoder())), encodingTestEvent{Value: "cbor"}))

		loaded, err := jsonStore.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, loaded.Events, 1)
		assert.Equal(t, we.CBOREncoding, loaded.Events[0].Data.Encoding)

		var decoded encodingTestEvent
		require.NoError(t, we.MakeCBORDecoder().Decode(loaded.Events[0].Data, &decoded))
		assert.Equal(t, "cbor", decoded.Value)
	})

	// ENCODING-S2.R3 (positive path) — a JSON override on a CBOR-constructed
	// store publishes successfully, proving the override is what encodes.
	t.Run("json override takes precedence over a cbor constructor encoder", func(t *testing.T) {
		id := makeEncodingAggregateId()

		require.NoError(t, cborStore.Publish(ctx, id, we.Options(we.WithEncoder(we.MakeJSONEncoder())), encodingTestEvent{Value: "json"}))

		loaded, err := cborStore.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, loaded.Events, 1)
		assert.Equal(t, we.JSONEncoding, loaded.Events[0].Data.Encoding)

		var decoded encodingTestEvent
		require.NoError(t, we.UnmarshalFromData(loaded.Events[0].Data, &decoded))
		assert.Equal(t, "json", decoded.Value)
	})
}
