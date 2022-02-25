package we

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/jaswdr/faker"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
)

var entropy = ulid.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)

func NewEventStoreValidationSuite(ctx context.Context, store EventStore) *EventStoreValidationSuite {
	faker := faker.New()
	return &EventStoreValidationSuite{
		store: store,
		ctx:   ctx,
		faker: faker,
	}
}

type EventStoreValidationSuite struct {
	store EventStore
	ctx   context.Context
	faker faker.Faker
}

type StoreValidationEvent struct {
	TestStringValue string `json:"test_string_value"`
	TestIntValue    int    `json:"test_int_value"`
}

func (s *EventStoreValidationSuite) Run(t *testing.T) {
	t.Run("loads an initial revision", s.LoadInitial)
	t.Run("loads a revision with events", s.LoadsRevisionWithEvents)
	t.Run("publishes single event", s.PublishesSingleEvent)
	t.Run("publishes multiple events in a single transcation", s.PublishesMultipleEvents)
	t.Run("returns a revision conflict with an initial revision", s.RevisionConflictOnInitialRevision)
	t.Run("returns a revision conflict on subsequent revision", s.RevisionConflictOnSubsequentRevision)
	t.Run("supports causation id", s.Causation)
}

func (s *EventStoreValidationSuite) MakeTestAggregateId() AggregateId {
	return AggregateId{
		Type: "go-test",
		Key:  ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String(),
	}
}

func (s *EventStoreValidationSuite) MakeTestEvent() StoreValidationEvent {
	return StoreValidationEvent{
		TestStringValue: s.faker.Lorem().Sentence(10),
		TestIntValue:    s.faker.Int(),
	}
}

func (s *EventStoreValidationSuite) MakeTestEvents(count int) []DomainEvent {
	events := make([]DomainEvent, count)
	for i := 0; i < count; i++ {
		events[i] = s.MakeTestEvent()
	}

	return events
}

func (s *EventStoreValidationSuite) LoadInitial(t *testing.T) {
	aggregateId := s.MakeTestAggregateId()
	aggregate, err := s.store.Load(
		s.ctx, aggregateId,
	)

	if !assert.Nil(t, err) {
		return
	}

	assert.Empty(t, aggregate.Events)
	assert.Equal(t, InitialRevision, aggregate.Revision)
	assert.EqualValues(t, aggregateId, aggregate.Id)
}

func (s *EventStoreValidationSuite) PublishesSingleEvent(t *testing.T) {
	event := s.MakeTestEvent()

	aggregateId := s.MakeTestAggregateId()
	err := s.store.Publish(s.ctx, aggregateId, Options(), event)

	assert.Nil(t, err)
}

func (s *EventStoreValidationSuite) PublishesMultipleEvents(t *testing.T) {
	events := s.MakeTestEvents(17)

	aggregateId := s.MakeTestAggregateId()
	err := s.store.Publish(s.ctx, aggregateId, Options(), events...)

	assert.Nil(t, err)
}

func (s *EventStoreValidationSuite) LoadsRevisionWithEvents(t *testing.T) {
	aggregateId := s.MakeTestAggregateId()
	event := s.MakeTestEvent()

	err := s.store.Publish(s.ctx, aggregateId, Options(), event)
	if !assert.Nil(t, err) {
		return
	}

	aggregate, err := s.store.Load(
		s.ctx, aggregateId,
	)
	if !assert.Nil(t, err) {
		return
	}

	assert.NotEmpty(t, aggregate.Events)
	assert.EqualValues(t, aggregateId, aggregate.Id)

}

func (s *EventStoreValidationSuite) Last(id AggregateId) (*RecordedEvent, error) {
	loaded, err := s.store.Load(s.ctx, id)
	if err != nil {
		return nil, err
	}

	length := len(loaded.Events)
	if length == 0 {
		return nil, errors.New("no events founds")
	}

	return &loaded.Events[length-1], nil
}

func (s *EventStoreValidationSuite) RevisionConflictOnInitialRevision(t *testing.T) {
	event := s.MakeTestEvent()

	aggregateId := s.MakeTestAggregateId()
	err := s.store.Publish(s.ctx, aggregateId, Options(), event)
	if !assert.Nil(t, err) {
		return
	}

	err = s.store.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(InitialRevision)), event)
	assert.NotNil(t, err)
	assert.Equal(t, RevisionConflict, err)

}

func (s *EventStoreValidationSuite) RevisionConflictOnSubsequentRevision(t *testing.T) {
	aggregateId := s.MakeTestAggregateId()
	event := s.MakeTestEvent()

	err := s.store.Publish(s.ctx, aggregateId, Options(), event)
	if !assert.Nil(t, err) {
		return
	}

	first, err := s.store.Load(s.ctx, aggregateId)
	if !assert.Nil(t, err) {
		return
	}

	err = s.store.Publish(s.ctx, aggregateId, Options(), event)
	if !assert.Nil(t, err) {
		return
	}

	err = s.store.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(first.Revision)), event)
	assert.NotNil(t, err)
	assert.Equal(t, RevisionConflict, err)

}

func (s *EventStoreValidationSuite) Causation(t *testing.T) {
	event := s.MakeTestEvent()

	aggregateId := s.MakeTestAggregateId()
	err := s.store.Publish(s.ctx, aggregateId, Options(), event)
	if !assert.Nil(t, err) {
		return
	}

	first, err := s.Last(aggregateId)
	if !assert.Nil(t, err) {
		return
	}

	correlationId := CorrelationID(strings.Join([]string{"event/", first.EventID.String()}, ""))

	err = s.store.Publish(
		s.ctx,
		aggregateId,
		Options(WithCausationId(correlationId, first.EventID)),
		event,
	)
	if !assert.Nil(t, err) {
		return
	}

	second, err := s.Last(aggregateId)
	if !assert.Nil(t, err) {
		return
	}

	assert.Equal(t, correlationId, second.Metadata.CorrelationId)
	assert.Equal(t, first.EventID, second.Metadata.CausationId)
}
