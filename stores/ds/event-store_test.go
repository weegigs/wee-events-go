package ds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/fxamacker/cbor/v2"
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
	// constructor encoder; with the envelope treating payload bytes as
	// opaque, a CBOR override on a JSON-constructed store round-trips
	// end-to-end.
	t.Run("cbor override round-trips on the json constructor store", func(t *testing.T) {
		id := createId()

		require.NoError(t, store.Publish(ctx, id, we.Options(we.WithEncoder(we.MakeCBOREncoder())), Tested{TestStringValue: "cbor", TestIntValue: 2}))

		loaded, err := store.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, loaded.Events, 1)
		assert.Equal(t, we.CBOREncoding, loaded.Events[0].Data.Encoding)

		var decoded Tested
		require.NoError(t, we.MakeCBORDecoder().Decode(loaded.Events[0].Data, &decoded))
		assert.Equal(t, "cbor", decoded.TestStringValue)
		assert.Equal(t, 2, decoded.TestIntValue)
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

	// ADR-0011 decision 4 — the storage encoding is store-chosen and optimal
	// for the medium: recorded events live as CBOR bytes in a native DynamoDB
	// binary (B) attribute, not a text envelope. Fetching the raw item pins
	// the layout mechanically: a regression to a string/JSON envelope changes
	// the attribute type or makes the bytes valid JSON.
	t.Run("storage layout is a cbor binary attribute", func(t *testing.T) {
		id := createId()

		first := Tested{TestStringValue: "layout", TestIntValue: 7}
		second := Tested{TestStringValue: "layout", TestIntValue: 8}
		require.NoError(t, store.Publish(ctx, id, we.Options(), first, second))

		out, err := store.db.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(store.table),
			KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":     &types.AttributeValueMemberS{Value: partitionKey(id)},
				":prefix": &types.AttributeValueMemberS{Value: "change-set#"},
			},
		})
		require.NoError(t, err)
		require.Len(t, out.Items, 1)

		attr, ok := out.Items[0]["events"]
		require.True(t, ok, "change-set item must carry an events attribute")

		binary, ok := attr.(*types.AttributeValueMemberB)
		require.True(t, ok, "events must be a binary (B) attribute, got %T", attr)
		assert.False(t, json.Valid(binary.Value), "stored envelope bytes must not be valid JSON")

		var recorded []we.RecordedEvent
		require.NoError(t, cbor.Unmarshal(binary.Value, &recorded))
		require.Len(t, recorded, 2)

		for index, want := range []Tested{first, second} {
			assert.Equal(t, id, recorded[index].AggregateId)
			assert.Equal(t, we.EventTypeOf(want), recorded[index].EventType)

			var decoded Tested
			require.NoError(t, we.MakeJSONDecoder().Decode(recorded[index].Data, &decoded))
			assert.Equal(t, want, decoded)
		}
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

// SURFACE-S2 — classification survives extra wrapping, skips nil reason
// codes, and never relabels a non-conflict.
func TestMaybeRevisionConflict(t *testing.T) {
	ccf := "ConditionalCheckFailed"
	tc := &types.TransactionCanceledException{
		CancellationReasons: []types.CancellationReason{
			{Code: nil}, // DynamoDB uses nil/None for non-blocking items
			{Code: &ccf},
		},
	}

	t.Run("deeply wrapped exception classifies", func(t *testing.T) {
		err := fmt.Errorf("operation: %w", fmt.Errorf("transport: %w", tc))
		assert.ErrorIs(t, maybeRevisionConflict(err), we.RevisionConflict)
	})

	t.Run("nil reason codes never panic", func(t *testing.T) {
		onlyNil := &types.TransactionCanceledException{
			CancellationReasons: []types.CancellationReason{{Code: nil}},
		}
		err := errors.New("wrapper")
		var result error
		assert.NotPanics(t, func() { result = maybeRevisionConflict(fmt.Errorf("%w", onlyNil)) })
		assert.NotErrorIs(t, result, we.RevisionConflict)
		assert.Equal(t, err, maybeRevisionConflict(err))
	})

	t.Run("wrapped sentinel still detected as conflict", func(t *testing.T) {
		assert.True(t, isRevisionConflict(fmt.Errorf("retry: %w", we.RevisionConflict)))
	})
}

// ENCODING-S2.R2 — a nil constructor encoder is an error, not a deferred panic.
func TestNilEncoderRejectedAtConstruction(t *testing.T) {
	store, err := NewEventStore(nil, EventStoreTableName("test-events"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ds: encoder is required")
	assert.Nil(t, store)
}
