package we

import (
	"context"
	"sync"
	"time"
)

// memoryBacking is the shared, in-memory backing that one or more
// memoryEventStore instances read from and write to. Two stores constructed
// over the same backing model the "two instances over one backing" arrangement
// the shared-backing suite exercises (CONFORMANCE-S5). The backing enforces the
// persistence contract the validation suite asserts: faithful byte storage,
// strictly ascending revisions, and optimistic concurrency control.
type memoryBacking struct {
	lk        sync.Mutex
	revisions *RevisionGenerator
	streams   map[EncodedAggregateId][]RecordedEvent
}

func newMemoryBacking() *memoryBacking {
	return &memoryBacking{
		revisions: NewRevisionGenerator(),
		streams:   map[EncodedAggregateId][]RecordedEvent{},
	}
}

func (b *memoryBacking) load(id AggregateId) Aggregate {
	b.lk.Lock()
	defer b.lk.Unlock()

	stored := b.streams[id.Encode()]
	events := make([]RecordedEvent, len(stored))
	copy(events, stored)

	revision := InitialRevision
	if len(events) > 0 {
		revision = events[len(events)-1].Revision
	}

	return Aggregate{
		Id:       id,
		Events:   events,
		Revision: revision,
	}
}

func (b *memoryBacking) publish(id AggregateId, options PublishOptions, events []DomainEvent) error {
	b.lk.Lock()
	defer b.lk.Unlock()

	encoded := id.Encode()
	stored := b.streams[encoded]

	// Optimistic concurrency: validate the expected revision against the current
	// head before any state change so a stale write is rejected, never partially
	// applied.
	if len(options.ExpectedRevision) > 0 {
		current := InitialRevision
		if len(stored) > 0 {
			current = stored[len(stored)-1].Revision
		}
		if options.ExpectedRevision != current {
			return RevisionConflict
		}
	}

	// "State is not an error": an empty publish records nothing and reports
	// success (CONFORMANCE-S3).
	if len(events) == 0 {
		return nil
	}

	now := time.Now()
	timestamp := Timestamp(now.UTC().Format(RFC3339Milli))

	recorded := make([]RecordedEvent, len(events))
	for i, event := range events {
		data, err := MarshalToData(event)
		if err != nil {
			return err
		}

		revision := b.revisions.NewRevision(now)
		recorded[i] = RecordedEvent{
			AggregateId: id,
			Revision:    revision,
			EventID:     EventID(revision),
			EventType:   EventTypeOf(event),
			Timestamp:   timestamp,
			Metadata:    options.RecordedEventMetadata,
			Data:        data,
		}
	}

	b.streams[encoded] = append(stored, recorded...)
	return nil
}

// memoryEventStore is an in-memory EventStore used to self-test the validation
// suite mechanism under `just test` without Docker. It is a faithful reference
// implementation of the persistence contract — not a production store.
type memoryEventStore struct {
	backing *memoryBacking
}

func newMemoryEventStore() *memoryEventStore {
	return &memoryEventStore{backing: newMemoryBacking()}
}

func (s *memoryEventStore) Load(_ context.Context, id AggregateId) (Aggregate, error) {
	return s.backing.load(id), nil
}

func (s *memoryEventStore) Publish(_ context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) error {
	return s.backing.publish(aggregateId, options, events)
}

var _ EventStore = (*memoryEventStore)(nil)
