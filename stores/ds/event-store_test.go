package ds

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
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
		assert.Equal(t, we.InitialRevision, loaded.Revision)
	})
}
