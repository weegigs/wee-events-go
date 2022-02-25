package main

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/weegigs/wee-events-go/samples/counter"
	"github.com/weegigs/wee-events-go/stores/ds"
	"github.com/weegigs/wee-events-go/we"
)

var entropy = ulid.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)

func createId() we.AggregateId {
	return we.AggregateId{
		Type: "go-test",
		Key:  ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String(),
	}
}

type test = func(t *testing.T)

func loadInitialCounter(controller we.EntityService[counter.Counter]) test {
	return func(t *testing.T) {
		ctx := context.TODO()

		entity, err := controller.Load(
			ctx, we.AggregateId{
				Type: "counter",
				Key:  "test-1",
			},
		)

		if err != nil {
			t.Logf("Unexpected failure %+v", err)
			t.Fail()
			return
		}

		assert.Equal(t, we.InitialRevision, entity.Revision)
		assert.Equal(t, false, entity.Initialized())
	}
}

func incrementsCounter(controller we.EntityService[counter.Counter]) test {
	return func(t *testing.T) {
		ctx := context.TODO()

		entity, err := controller.Execute(
			ctx, we.AggregateId{
				Type: "counter",
				Key:  "test-2",
			},
			counter.Increment{
				Amount: 7,
			},
		)

		if err != nil {
			t.Logf("Unexpected failure %+v", err)
			t.Fail()
			return
		}

		assert.NotEqual(t, we.InitialRevision, entity.Revision)
		assert.Equal(t, true, entity.Initialized())
		assert.Equal(t, 7, entity.State.Value())
	}
}

func decrementsCounter(controller we.EntityService[counter.Counter]) test {
	return func(t *testing.T) {
		ctx := context.TODO()

		_, err := controller.Execute(
			ctx, we.AggregateId{
				Type: "counter",
				Key:  "test-3",
			},
			counter.Increment{
				Amount: 7,
			},
		)

		if err != nil {
			t.Logf("Unexpected failure %+v", err)
			t.Fail()
			return
		}

		entity, err := controller.Execute(
			ctx, we.AggregateId{
				Type: "counter",
				Key:  "test-2",
			},
			counter.Decrement{
				Amount: 5,
			},
		)

		if err != nil {
			t.Logf("Unexpected failure %+v", err)
			t.Fail()
			return
		}

		assert.NotEqual(t, we.InitialRevision, entity.Revision)
		assert.Equal(t, true, entity.Initialized())
		assert.Equal(t, 2, entity.State.Value())
	}
}

func TestCounterController(t *testing.T) {
	store, teardown, err := ds.DynamoTestStore(context.Background())
	if err != nil {
		t.Logf("failed to initiate test store: %+v", err)
		t.Fail()
		return
	}

	defer teardown()
	controller := NewCounterService(store, func() int { return 1 })

	t.Run("load initial entity", loadInitialCounter(controller))
	t.Run("increment counter", incrementsCounter(controller))
	t.Run("decrement counter", decrementsCounter(controller))
}
