package dynamo

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	we "github.com/weegigs/wee-events-go"
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
	eventStore, tearDown, err := DynamoTestStore(ctx)
	if err != nil {
		t.Fatalf("failed to create test store. %+v", err)
	}

	defer tearDown()

	loadInitial := func(t *testing.T) {
		aggregateId := createId()
		aggregate, err := eventStore.Load(
			ctx, aggregateId,
		)

		if !assert.Nil(t, err) {
			return
		}

		assert.Empty(t, aggregate.Events)
		assert.Equal(t, we.InitialRevision, aggregate.Revision)
		assert.EqualValues(t, aggregateId, aggregate.Id)
	}

	basePublish := func(t *testing.T) {
		event := Tested{
			TestStringValue: "test string",
			TestIntValue:    42,
		}

		aggregateId := createId()
		published, err := eventStore.Publish(ctx, aggregateId, we.Options(), event)
		if !assert.Nil(t, err) {
			return
		}

		assert.NotNil(t, published)

		_, err = eventStore.Remove(ctx, aggregateId)
		if !assert.Nil(t, err) {
			return
		}
	}

	loadsRevisionWithEvents := func(t *testing.T) {
		aggregateId := createId()
		event := Tested{
			TestStringValue: "test string",
			TestIntValue:    42,
		}

		published, err := eventStore.Publish(ctx, aggregateId, we.Options(), event)
		if !assert.Nil(t, err) {
			return
		}

		aggregate, err := eventStore.Load(
			ctx, aggregateId,
		)
		if !assert.Nil(t, err) {
			return
		}

		assert.NotEmpty(t, aggregate.Events)
		assert.Equal(t, published, aggregate.Revision)
		assert.EqualValues(t, aggregateId, aggregate.Id)

		_, err = eventStore.Remove(ctx, aggregateId)
		if !assert.Nil(t, err) {
			return
		}
	}

	lastEvent := func(id we.AggregateId) (*we.RecordedEvent, error) {
		loaded, err := eventStore.Load(ctx, id)
		if err != nil {
			return nil, err
		}

		length := len(loaded.Events)
		if length == 0 {
			return nil, errors.New("no events founds")
		}

		return &loaded.Events[length-1], nil
	}

	RevisionConflictOnInitialRevision := func(t *testing.T) {
		event := Tested{
			TestStringValue: "test string",
			TestIntValue:    42,
		}

		aggregateId := createId()
		_, err := eventStore.Publish(ctx, aggregateId, we.Options(), event)
		if !assert.Nil(t, err) {
			return
		}

		_, err = eventStore.Publish(ctx, aggregateId, we.Options(we.WithExpectedRevision(we.InitialRevision)), event)
		assert.NotNil(t, err)
		assert.Equal(t, we.RevisionConflict, err)

		_, err = eventStore.Remove(ctx, aggregateId)
		if !assert.Nil(t, err) {
			return
		}
	}

	RevisionConflictOnSubsequentRevision := func(t *testing.T) {
		event := Tested{
			TestStringValue: "test string",
			TestIntValue:    42,
		}

		aggregateId := createId()
		one, err := eventStore.Publish(ctx, aggregateId, we.Options(), event)
		if !assert.Nil(t, err) {
			return
		}

		_, err = eventStore.Publish(ctx, aggregateId, we.Options(), event)
		if !assert.Nil(t, err) {
			return
		}

		_, err = eventStore.Publish(ctx, aggregateId, we.Options(we.WithExpectedRevision(one)), event)
		assert.NotNil(t, err)
		assert.Equal(t, we.RevisionConflict, err)

		_, err = eventStore.Remove(ctx, aggregateId)
		if !assert.Nil(t, err) {
			return
		}
	}

	causation := func(t *testing.T) {
		event := Tested{
			TestStringValue: "test string",
			TestIntValue:    42,
		}

		aggregateId := createId()
		_, err := eventStore.Publish(ctx, aggregateId, we.Options(), event)
		if !assert.Nil(t, err) {
			return
		}

		first, err := lastEvent(aggregateId)
		if !assert.Nil(t, err) {
			return
		}

		correlationId := we.CorrelationID(strings.Join([]string{"event/", first.EventID.String()}, ""))

		_, err = eventStore.Publish(
			ctx,
			aggregateId,
			we.Options(we.WithCausationId(correlationId, first.EventID)),
			event,
		)
		if !assert.Nil(t, err) {
			return
		}

		second, err := lastEvent(aggregateId)
		if !assert.Nil(t, err) {
			return
		}

		assert.Equal(t, correlationId, second.Metadata.CorrelationId)
		assert.Equal(t, first.EventID, second.Metadata.CausationId)
	}

	removeEntity := func(t *testing.T) {
		event := Tested{
			TestStringValue: "test string",
			TestIntValue:    42,
		}
		aggregateId := createId()

		_, err := eventStore.Publish(ctx, aggregateId, we.Options(), event)
		if !assert.Nil(t, err) {
			return
		}

		count, err := eventStore.Remove(ctx, aggregateId)
		if !assert.Nil(t, err) {
			return
		}

		assert.Equal(t, 2, count)

		loaded, err := eventStore.Load(ctx, aggregateId)
		assert.Equal(t, we.InitialRevision, loaded.Revision)
	}

	t.Run("loads an initial Revision", loadInitial)
	t.Run("loads a Revision with events", loadsRevisionWithEvents)
	t.Run("base publish", basePublish)
	t.Run("Revision conflict on initial Revision", RevisionConflictOnInitialRevision)
	t.Run("Revision conflict on subsequent Revision", RevisionConflictOnSubsequentRevision)
	t.Run("supports causation id", causation)
	t.Run("removes details for entities", removeEntity)
}
