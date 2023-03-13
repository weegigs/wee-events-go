package esdbs

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/weegigs/wee-events-go/we"
)

type TestEvent struct {
	Value string `json:"value"`
}

func TestEventStore(t *testing.T) {
	ctx := context.Background()
	store, cleanup, err := NewESDBTestStore(ctx, PageSize(5))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	t.Run("esdb event store validation", func(t *testing.T) {
		suite := we.NewEventStoreValidationSuite(ctx, store)
		suite.Run(t)
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

func createEvents(count uint) []we.DomainEvent {
	events := make([]we.DomainEvent, count)
	for i := range events {
		events[i] = TestEvent{Value: fmt.Sprintf("test %d", i)}
	}
	return events
}
