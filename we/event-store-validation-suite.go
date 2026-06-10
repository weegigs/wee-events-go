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
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

var entropy = ulid.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)

func NewEventStoreValidationSuite(ctx context.Context, store EventStore) *EventStoreValidationSuite {
	f := faker.New()
	return &EventStoreValidationSuite{
		store: store,
		ctx:   ctx,
		faker: f,
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

// scenario pairs a subtest name with the suite method that implements it.
type scenario struct {
	name string
	run  func(t *testing.T)
}

// scenarios returns the ordered set of single-store scenarios Run registers.
// Exposing the set (rather than only calling t.Run inline) lets the suite's own
// self-tests assert which scenarios are registered (CONFORMANCE-S1.R1/R3)
// without faking testing.T, which is a concrete type.
func (s *EventStoreValidationSuite) scenarios() []scenario {
	return []scenario{
		{"loads an initial revision", s.LoadInitial},
		{"loads a revision with events", s.LoadsRevisionWithEvents},
		{"publishes single event", s.PublishesSingleEvent},
		{"publishes multiple events in a single transaction", s.PublishesMultipleEvents},
		{"preserves the event content when recording", s.ValidateEventContent},
		{"published with an expected initial revision", s.PublishesWithAnExpectedInitialRevision},
		{"published with an expected revision", s.PublishesWithAnExpectedRevision},
		{"published with an expected revision past ten events", s.PublishesWithAnExpectedRevisionPastTenEvents},
		{"returns a revision conflict with an initial revision", s.RevisionConflictOnInitialRevision},
		{"returns a revision conflict on subsequent revision", s.RevisionConflictOnSubsequentRevision},
		{"detects a stale revision and a retry then succeeds", s.StaleRevisionDetectedAndRetrySucceeds},
		{"treats an empty publish as a no-op returning the current revision", s.EmptyPublishReturnsCurrentRevision},
		{"preserves event ordering across separately published batches", s.EventOrderingPreserved},
		{"supports causation id", s.Causation},
		{"round-trips full-charset identities through storage", s.IdentityRoundTripsThroughStorage},
	}
}

func (s *EventStoreValidationSuite) Run(t *testing.T) {
	for _, scenario := range s.scenarios() {
		t.Run(scenario.name, scenario.run)
	}
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
	for i := range count {
		events[i] = s.MakeTestEvent()
	}

	return events
}

func (s *EventStoreValidationSuite) LoadAggregate(id AggregateId) (Aggregate, error) {
	return s.store.Load(
		s.ctx, id,
	)
}

func (s *EventStoreValidationSuite) ExpectEventCount(t *testing.T, id AggregateId, count int) error {
	aggregate, err := s.LoadAggregate(id)
	if err != nil {
		return err
	}

	assert.Equal(t, count, len(aggregate.Events))

	return nil
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

	err = s.ExpectEventCount(t, aggregateId, 1)
	assert.Nil(t, err)
}

func (s *EventStoreValidationSuite) PublishesMultipleEvents(t *testing.T) {
	events := s.MakeTestEvents(17)

	aggregateId := s.MakeTestAggregateId()
	err := s.store.Publish(s.ctx, aggregateId, Options(), events...)

	assert.Nil(t, err)
	err = s.ExpectEventCount(t, aggregateId, 17)
	assert.Nil(t, err)
}

func (s *EventStoreValidationSuite) ValidateEventContent(t *testing.T) {
	events := s.MakeTestEvents(17)

	aggregateId := s.MakeTestAggregateId()
	err := s.store.Publish(s.ctx, aggregateId, Options(), events...)

	assert.Nil(t, err)

	aggregate, err := s.LoadAggregate(aggregateId)
	assert.Nil(t, err)

	require.Equal(t, len(events), len(aggregate.Events))
	for i, event := range events {
		assert.Equal(t, EventTypeOf(event), aggregate.Events[i].EventType)

		e := StoreValidationEvent{}
		err := UnmarshalFromData(aggregate.Events[i].Data, &e)
		require.NoError(t, err)
		assert.Equal(t, event, e)
	}
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

func (s *EventStoreValidationSuite) PublishesWithAnExpectedInitialRevision(t *testing.T) {
	event := s.MakeTestEvent()

	aggregateId := s.MakeTestAggregateId()

	err := s.store.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(InitialRevision)), event)
	assert.Nil(t, err)

	err = s.ExpectEventCount(t, aggregateId, 1)
	assert.Nil(t, err)
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

func (s *EventStoreValidationSuite) PublishesWithAnExpectedRevision(t *testing.T) {
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

	err = s.store.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(first.Revision)), event)
	assert.Nil(t, err)

	err = s.ExpectEventCount(t, aggregateId, 2)
	assert.Nil(t, err)
}

// PublishesWithAnExpectedRevisionPastTenEvents guards against revision
// encode/decode base mismatches: a store that records revisions in one base
// but parses an expected revision in another silently breaks once the
// revision exceeds a single hex digit (revision 10 is 0x0a). Seeding twelve
// events forces that boundary before the expected-revision round-trip.
func (s *EventStoreValidationSuite) PublishesWithAnExpectedRevisionPastTenEvents(t *testing.T) {
	aggregateId := s.MakeTestAggregateId()

	err := s.store.Publish(s.ctx, aggregateId, Options(), s.MakeTestEvents(12)...)
	if !assert.Nil(t, err) {
		return
	}

	loaded, err := s.store.Load(s.ctx, aggregateId)
	if !assert.Nil(t, err) {
		return
	}

	err = s.store.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(loaded.Revision)), s.MakeTestEvent())
	assert.Nil(t, err)

	err = s.ExpectEventCount(t, aggregateId, 13)
	assert.Nil(t, err)
}

// StaleRevisionDetectedAndRetrySucceeds exercises the optimistic-retry path end
// to end (CONFORMANCE-S2). The aggregate is loaded, a concurrent write advances
// its revision, a publish carrying the now-stale expected revision must conflict,
// and a reload-and-retry with the fresh expected revision must then succeed. The
// store leaves three events in strictly ascending order.
func (s *EventStoreValidationSuite) StaleRevisionDetectedAndRetrySucceeds(t *testing.T) {
	aggregateId := s.MakeTestAggregateId()

	// Seed the aggregate so a non-initial revision is observed.
	err := s.store.Publish(s.ctx, aggregateId, Options(), s.MakeTestEvent())
	require.NoError(t, err)

	// The caller loads the aggregate and remembers the revision it intends to
	// build on.
	loaded, err := s.store.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	staleRevision := loaded.Revision

	// A concurrent writer advances the aggregate, making staleRevision stale.
	err = s.store.Publish(s.ctx, aggregateId, Options(), s.MakeTestEvent())
	require.NoError(t, err)

	// CONFORMANCE-S2.R1 / R4: publishing against the stale expected revision must
	// fail with a revision conflict; accepting it would silently drop the
	// concurrent commit.
	err = s.store.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(staleRevision)), s.MakeTestEvent())
	require.Error(t, err)
	require.Equal(t, RevisionConflict, err)

	// The conflict must not have recorded anything.
	require.NoError(t, s.ExpectEventCount(t, aggregateId, 2))

	// CONFORMANCE-S2.R2: reload to obtain the fresh expected revision and retry.
	reloaded, err := s.store.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	require.NotEqual(t, staleRevision, reloaded.Revision)

	err = s.store.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(reloaded.Revision)), s.MakeTestEvent())
	require.NoError(t, err)

	// CONFORMANCE-S2.R3: the aggregate now holds all three events in ascending
	// revision order.
	final, err := s.store.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	require.Equal(t, 3, len(final.Events))
	assertAscendingRevisions(t, final.Events)
}

// EmptyPublishReturnsCurrentRevision treats publishing an empty event list as a
// no-op state outcome rather than an error (CONFORMANCE-S3). Since
// EventStore.Publish returns only error, the unchanged revision and event set
// are verified through a follow-up Load.
func (s *EventStoreValidationSuite) EmptyPublishReturnsCurrentRevision(t *testing.T) {
	aggregateId := s.MakeTestAggregateId()

	err := s.store.Publish(s.ctx, aggregateId, Options(), s.MakeTestEvents(3)...)
	require.NoError(t, err)

	before, err := s.store.Load(s.ctx, aggregateId)
	require.NoError(t, err)

	// CONFORMANCE-S3.R1 / R3: an empty publish is a no-op and must not error.
	err = s.store.Publish(s.ctx, aggregateId, Options())
	require.NoError(t, err)

	// CONFORMANCE-S3.R2 / R3: revision and event set are unchanged by the no-op.
	after, err := s.store.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	assert.Equal(t, before.Revision, after.Revision)
	require.Equal(t, len(before.Events), len(after.Events))
	for i := range before.Events {
		assert.Equal(t, before.Events[i].Revision, after.Events[i].Revision)
		assert.Equal(t, before.Events[i].EventID, after.Events[i].EventID)
	}

	// CONFORMANCE-S3.R3: the no-op must also leave optimistic concurrency
	// intact — a publish conditioned on the loaded revision must still succeed.
	// A backend whose empty publish advances hidden backing-store state (e.g. a
	// stream sequence) passes the Load comparison above but fails here with a
	// spurious revision conflict.
	err = s.store.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(after.Revision)), s.MakeTestEvent())
	require.NoError(t, err)
	require.NoError(t, s.ExpectEventCount(t, aggregateId, 4))
}

// EventOrderingPreserved asserts that events recorded across separately
// published batches load back in strictly ascending revision order with no
// duplicates (CONFORMANCE-S4).
func (s *EventStoreValidationSuite) EventOrderingPreserved(t *testing.T) {
	aggregateId := s.MakeTestAggregateId()

	// Three separate publish calls, so ordering must hold across batch
	// boundaries rather than only within a single transaction.
	require.NoError(t, s.store.Publish(s.ctx, aggregateId, Options(), s.MakeTestEvents(2)...))
	require.NoError(t, s.store.Publish(s.ctx, aggregateId, Options(), s.MakeTestEvents(3)...))
	require.NoError(t, s.store.Publish(s.ctx, aggregateId, Options(), s.MakeTestEvent()))

	loaded, err := s.store.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	require.Equal(t, 6, len(loaded.Events))
	assertAscendingRevisions(t, loaded.Events)
}

// assertAscendingRevisions fails the test if the recorded events are not in
// strictly ascending revision order or if any revision is duplicated
// (CONFORMANCE-S2.R3, CONFORMANCE-S4.R1/R2).
func assertAscendingRevisions(t *testing.T, events []RecordedEvent) {
	t.Helper()
	for i := 1; i < len(events); i++ {
		previous := events[i-1].Revision
		current := events[i].Revision
		assert.NotEqual(t, previous, current, "duplicate revision %s at index %d", current, i)
		assert.Less(t, previous.String(), current.String(),
			"revision %s at index %d is not strictly greater than %s at index %d",
			current, i, previous, i-1)
	}
}

// IdentityRoundTripsThroughStorage proves a backend stores and returns the
// exact aggregate identity and payload for property-generated inputs across
// the full charset grammar of both identity parts — generated types over the
// RFC 3986 unreserved runes, generated keys additionally including the '|'
// composite separator — and arbitrary unicode payload content
// (IDENTITY-S4.R3, ADR-0009). This is the binding form of "stores adapt to
// the key space" (IDENTITY-S4.R2): a backend that rejects, transforms,
// truncates, or re-derives identity or payload fails here, whatever its
// transport — present or future. Failures shrink to a minimal
// identity/payload pair.
func (s *EventStoreValidationSuite) IdentityRoundTripsThroughStorage(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// A fresh ULID suffix keeps generated identities unique per iteration
		// against the shared backing store.
		base := s.MakeTestAggregateId()
		typ := IdentityTypeGen().Draw(rt, "type")
		key := IdentityKeyGen().Draw(rt, "key")
		id, err := MakeAggregateId(typ, key+"|"+base.Key)
		require.NoError(rt, err)

		event := StoreValidationEvent{
			TestStringValue: rapid.String().Draw(rt, "payload"), // arbitrary unicode, including empty
			TestIntValue:    rapid.Int().Draw(rt, "count"),
		}
		require.NoError(rt, s.store.Publish(s.ctx, id, Options(), event))

		loaded, err := s.store.Load(s.ctx, id)
		require.NoError(rt, err)
		require.Len(rt, loaded.Events, 1)
		require.Equal(rt, id, loaded.Events[0].AggregateId)

		var decoded StoreValidationEvent
		require.NoError(rt, UnmarshalFromData(loaded.Events[0].Data, &decoded))
		require.Equal(rt, event, decoded, "payload must round-trip verbatim")
	})
}

// NewSharedBackingSuite builds a paired conformance suite over two EventStore
// instances that share one backing (CONFORMANCE-S5.R1). A backend opts in by
// constructing two instances over the same store — a SQLite file, the same
// DynamoDB table, the same Kurrent/NATS server — and calling Run.
func NewSharedBackingSuite(ctx context.Context, a, b EventStore) *SharedBackingSuite {
	return &SharedBackingSuite{
		a:     a,
		b:     b,
		ctx:   ctx,
		faker: faker.New(),
	}
}

type SharedBackingSuite struct {
	a     EventStore
	b     EventStore
	ctx   context.Context
	faker faker.Faker
}

func (s *SharedBackingSuite) scenarios() []scenario {
	return []scenario{
		{"blind appends succeed across store instances", s.BlindAppendsSucceedAcrossStoreInstances},
		{"stale revision conflicts across store instances", s.StaleRevisionConflictsAcrossStoreInstances},
	}
}

func (s *SharedBackingSuite) Run(t *testing.T) {
	for _, scenario := range s.scenarios() {
		t.Run(scenario.name, scenario.run)
	}
}

func (s *SharedBackingSuite) makeTestAggregateId() AggregateId {
	return AggregateId{
		Type: "go-test",
		Key:  ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String(),
	}
}

func (s *SharedBackingSuite) makeTestEvent() StoreValidationEvent {
	return StoreValidationEvent{
		TestStringValue: s.faker.Lorem().Sentence(10),
		TestIntValue:    s.faker.Int(),
	}
}

// BlindAppendsSucceedAcrossStoreInstances verifies that two independent
// instances over one backing both persist their writes and each observes the
// other's commits on a subsequent Load (CONFORMANCE-S5.R2 / R5).
func (s *SharedBackingSuite) BlindAppendsSucceedAcrossStoreInstances(t *testing.T) {
	aggregateId := s.makeTestAggregateId()

	require.NoError(t, s.a.Publish(s.ctx, aggregateId, Options(), s.makeTestEvent()))
	require.NoError(t, s.b.Publish(s.ctx, aggregateId, Options(), s.makeTestEvent()))

	// Each instance must see both commits, regardless of which instance wrote.
	fromA, err := s.a.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	assert.Equal(t, 2, len(fromA.Events))
	assertAscendingRevisions(t, fromA.Events)

	fromB, err := s.b.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	assert.Equal(t, 2, len(fromB.Events))
	assertAscendingRevisions(t, fromB.Events)

	assert.Equal(t, fromA.Revision, fromB.Revision)
}

// StaleRevisionConflictsAcrossStoreInstances verifies that a revision loaded
// from instance A conflicts after instance B advances the aggregate
// (CONFORMANCE-S5.R3 / R5).
func (s *SharedBackingSuite) StaleRevisionConflictsAcrossStoreInstances(t *testing.T) {
	aggregateId := s.makeTestAggregateId()

	require.NoError(t, s.a.Publish(s.ctx, aggregateId, Options(), s.makeTestEvent()))

	// Instance A loads the current revision and intends to build on it.
	loaded, err := s.a.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	staleRevision := loaded.Revision

	// Instance B advances the aggregate over the shared backing.
	require.NoError(t, s.b.Publish(s.ctx, aggregateId, Options(), s.makeTestEvent()))

	// A's publish carrying the now-stale expected revision must conflict.
	err = s.a.Publish(s.ctx, aggregateId, Options(WithExpectedRevision(staleRevision)), s.makeTestEvent())
	require.Error(t, err)
	require.Equal(t, RevisionConflict, err)

	// The conflict must not have recorded A's event; only the two committed
	// events remain.
	final, err := s.b.Load(s.ctx, aggregateId)
	require.NoError(t, err)
	assert.Equal(t, 2, len(final.Events))
}
