package ds

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/weegigs/wee-events-go/we"
)

var entropy = ulid.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)

func createId() we.AggregateId {
	return we.AggregateId{
		Type: "go-test",
		Key:  ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String(),
	}
}

var TestedEvent = we.EventType("test:test-event")

type Tested struct {
	TestStringValue string `json:"test_string_value"`
	TestIntValue    int    `json:"test_int_value"`
}

func (Tested) EventType() we.EventType {
	return TestedEvent
}

func TestDynamoDBStore(t *testing.T) {
	ctx := context.Background()
	store, tearDown, err := DynamoTestStore(ctx)
	if err != nil {
		t.Logf("failed to create test store. %+v", err)
		t.FailNow()
	}

	defer tearDown()

	t.Run("esdb event store validation", func(t *testing.T) {
		suite := we.NewEventStoreValidationSuite(ctx, store)
		suite.Run(t)
	})

	t.Run("dynamodb shared-backing validation", func(t *testing.T) {
		// A second instance over the same client and table shares one backing.
		second, err := NewEventStore(store.db, EventStoreTableName(store.table), we.MakeJSONEncoder())
		require.NoError(t, err)
		suite := we.NewSharedBackingSuite(ctx, store, second)
		suite.Run(t)
	})

	// ENCODING-S2.R5 — an explicit nil override errors and records nothing.
	t.Run("nil encoder override is rejected and records nothing", func(t *testing.T) {
		id := createId()

		err := store.Publish(ctx, id, we.Options(we.WithEncoder(nil)), Tested{TestStringValue: "x"})
		require.Error(t, err)
		require.ErrorIs(t, err, we.NilEncoder)
		assert.Contains(t, err.Error(), "ds: encoder must not be nil")

		loaded, err := store.Load(ctx, id)
		require.NoError(t, err)
		assert.Empty(t, loaded.Events)
	})

	// ENCODING-S2.R3 — the per-publish override takes precedence over the
	// constructor encoder. The DynamoDB change set is a JSON transport (recorded
	// events are json.Marshal'ed into the record), so honouring a CBOR override
	// fails loudly and records nothing — end-to-end CBOR remains scoped to
	// BLOB-backed stores (ENCODING-S3.R2). The loud failure is itself the
	// precedence proof: the constructor's JSON encoder would have succeeded.
	t.Run("cbor override takes precedence over the json constructor encoder", func(t *testing.T) {
		id := createId()

		require.NoError(t, store.Publish(ctx, id, we.Options(), Tested{TestStringValue: "json", TestIntValue: 1}))

		err := store.Publish(ctx, id, we.Options(we.WithEncoder(we.MakeCBOREncoder())), Tested{TestStringValue: "cbor", TestIntValue: 2})
		require.Error(t, err)

		loaded, err := store.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, loaded.Events, 1)
		assert.Equal(t, we.JSONEncoding, loaded.Events[0].Data.Encoding)
	})

	// ENCODING-S2.R3 (positive path) — a JSON override on a CBOR-constructed
	// store publishes successfully, proving the override is what encodes.
	t.Run("json override takes precedence over a cbor constructor encoder", func(t *testing.T) {
		cborStore, err := NewEventStore(store.db, EventStoreTableName(store.table), we.MakeCBOREncoder())
		require.NoError(t, err)

		id := createId()
		require.NoError(t, cborStore.Publish(ctx, id, we.Options(we.WithEncoder(we.MakeJSONEncoder())), Tested{TestStringValue: "json", TestIntValue: 3}))

		loaded, err := cborStore.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, loaded.Events, 1)
		assert.Equal(t, we.JSONEncoding, loaded.Events[0].Data.Encoding)
	})

	t.Run("removes details for entities", func(t *testing.T) {
		event := Tested{
			TestStringValue: "test string",
			TestIntValue:    42,
		}
		aggregateId := createId()

		err := store.Publish(ctx, aggregateId, we.Options(), event)
		if !assert.Nil(t, err) {
			return
		}

		count, err := store.Remove(ctx, aggregateId)
		if !assert.Nil(t, err) {
			return
		}

		assert.Equal(t, 2, count)

		loaded, err := store.Load(ctx, aggregateId)
		if !assert.Nil(t, err) {
			return
		}
		assert.Equal(t, we.InitialRevision, loaded.Revision)
	})
}

// ENCODING-S2.R2 — a nil constructor encoder is an error, not a deferred panic.
func TestNilEncoderRejectedAtConstruction(t *testing.T) {
	store, err := NewEventStore(nil, EventStoreTableName("test-events"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ds: encoder is required")
	assert.Nil(t, store)
}
