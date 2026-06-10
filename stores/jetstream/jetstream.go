package jetstream

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/oklog/ulid/v2"
	"github.com/weegigs/wee-events-go/internal"
	"github.com/weegigs/wee-events-go/we"
)

type EventStoreOption func(*EventStore)

const prefix = "change-set."

// NewEventStore builds a JetStream-backed event store. The encoder is the
// store's explicit write encoding (ENCODING-S2.R1); nil is a construction
// error, never a deferred nil-dereference at first publish (ENCODING-S2.R2).
// The change-set message is a JSON transport: a non-JSON encoder constructs
// successfully but fails every non-empty publish loudly at serialization —
// end-to-end CBOR is scoped to BLOB-backed stores (ENCODING-S3.R2).
func NewEventStore(ctx context.Context, name string, connection *nats.Conn, encoder we.Encoder, options ...EventStoreOption) (*EventStore, error) {
	if encoder == nil {
		return nil, errors.New("jetstream: encoder is required")
	}

	js, err := jetstream.New(connection)
	if err != nil {
		return nil, err
	}

	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        name,
		Description: "change set stream for " + name,
		Subjects:    []string{prefix + ">"},
	})
	if err != nil {
		return nil, err
	}

	store := &EventStore{
		name:    name,
		js:      js,
		stream:  stream,
		encoder: encoder,
	}

	for _, option := range options {
		option(store)
	}

	if store.clock == nil {
		store.clock = defaultClock{}
	}

	if store.id == nil {
		store.id = NewDefaultIdGenerator(store.clock)
	}

	if store.marshaller == nil {
		store.marshaller = JSONMarshaller{}
	}

	return store, nil
}

type EventStore struct {
	name       string
	js         jetstream.JetStream
	stream     jetstream.Stream
	clock      Clock
	id         IDGenerator
	marshaller Marshaller
	encoder    we.Encoder
}

// subject derives the NATS subject for an aggregate from the canonical
// encoded identity (IDENTITY-S4.R2). NATS reserves '.' as the subject token
// separator, so a key containing '.' (a legal identity rune) would split into
// tokens — or produce empty ones — and be rejected by the server. The
// store-local encoding maps '.' to "%2E"; the mapping is deterministic and
// injective because '%' lies outside the identity charset and therefore never
// occurs in the canonical form. The remaining canonical runes (ALPHA, DIGIT,
// '-', '_', '~', '|', and the ':' joiner) are all legal NATS token characters.
func subject(aggregateId we.AggregateId) string {
	return prefix + strings.ReplaceAll(aggregateId.Encode().String(), ".", "%2E")
}

func (es *EventStore) Publish(ctx context.Context, aggregateId we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	if len(events) == 0 {
		// An empty publish is a no-op, not an error (CONFORMANCE-S3). Publishing
		// an empty changeset message would advance the subject's last sequence
		// past the revision a Load reports, breaking every subsequent
		// expected-revision publish with a spurious conflict.
		// The no-op deliberately short-circuits BEFORE override validation: with
		// zero events the nil encoder is never used and nothing can be
		// mis-recorded, so an empty publish is a state, not an error.
		return nil
	}

	// SURFACE-S4.R1 — the per-changeset event index is a uint16, so a changeset
	// beyond 65536 events would silently wrap the index and mint duplicate
	// revisions. Like the empty-publish check above, this is a structural
	// verdict on the changeset's shape: it needs no encoder, so it runs before
	// encoder resolution rather than after it.
	if len(events) > math.MaxUint16+1 {
		return fmt.Errorf("jetstream: changeset exceeds maximum batch size %d", math.MaxUint16+1)
	}

	// Resolve the publish encoder before encoding: an explicit per-publish
	// override wins, and an explicit nil override is an error that records
	// nothing (ENCODING-S2.R3, ENCODING-S2.R5).
	encoder, err := options.EncoderFor(es.encoder)
	if err != nil {
		return fmt.Errorf("jetstream: %w", err)
	}

	records := make([]EventRecord, len(events))

	for index, event := range events {
		data, err := we.MarshalToData(encoder, event)
		if err != nil {
			return err
		}
		records[index] = EventRecord{
			EventID:     es.id.Create(),
			EventType:   we.EventTypeOf(event),
			AggregateId: aggregateId,
			Data:        data,
			Metadata:    options.RecordedEventMetadata,
		}
	}

	changeset := ChangeSet{Events: records}
	bytes, err := es.marshaller.Marshal(changeset)
	if err != nil {
		return err
	}

	var opts []jetstream.PublishOpt

	expected := options.ExpectedRevision
	if expected != "" {
		if expected == we.InitialRevision {
			opts = append(opts, jetstream.WithExpectLastSequencePerSubject(0))
		} else {
			sequenceNumber, err := internal.DecodeSequenceNumber(expected)
			if err != nil {
				return err
			}

			opts = append(opts, jetstream.WithExpectLastSequencePerSubject(sequenceNumber))
		}
	}

	_, err = es.js.Publish(ctx, subject(aggregateId), bytes, opts...)
	if err != nil {
		var api *jetstream.APIError
		if errors.As(err, &api) && api.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence {
			return we.RevisionConflict
		}
		return err
	}

	return nil
}

func (es *EventStore) Load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	var events []we.RecordedEvent

	events, err := es.read(ctx, subject(id))
	if err != nil {
		return we.Aggregate{}, err
	}

	var revision we.Revision
	if len(events) == 0 {
		revision = we.InitialRevision
	} else {
		revision = events[len(events)-1].Revision
	}

	return we.Aggregate{
		Id:       id,
		Events:   events,
		Revision: revision,
	}, nil
}

func (es *EventStore) latest(ctx context.Context, subject string) (*uint64, error) {
	msg, err := es.stream.GetLastMsgForSubject(ctx, subject)
	if err != nil {
		if errors.Is(err, jetstream.ErrMsgNotFound) {
			return nil, nil
		}

		return nil, err
	}

	return &msg.Sequence, nil
}

func (es *EventStore) read(ctx context.Context, subject string) ([]we.RecordedEvent, error) {
	latest, err := es.latest(ctx, subject)
	if err != nil {
		return nil, err
	}

	if latest == nil {
		return nil, nil
	}

	consumer, err := es.stream.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{subject},
		DeliverPolicy:  jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return nil, err
	}

	messages, err := consumer.Messages()
	if err != nil {
		return nil, err
	}
	defer messages.Stop()

	var events []we.RecordedEvent
	for {
		msg, err := messages.Next()
		if err != nil {
			return nil, err
		}

		metadata, err := msg.Metadata()
		if err != nil {
			return nil, err
		}

		recorded, err := es.decodeChangeSet(msg.Data(), metadata)
		if err != nil {
			return nil, err
		}

		events = append(events, recorded...)

		// Ordered consumers use AckNonePolicy, so messages are not acked.

		if metadata.Sequence.Stream >= *latest {
			break
		}
	}

	return events, nil
}

func (es *EventStore) decodeChangeSet(data []byte, metadata *jetstream.MsgMetadata) ([]we.RecordedEvent, error) {
	cs := &ChangeSet{}
	err := es.marshaller.Unmarshal(data, cs)
	if err != nil {
		return nil, err
	}

	// SURFACE-S4.R2 — a stored changeset written by a foreign writer (one
	// without the publish-side guard) could exceed the uint16 index space;
	// decoding it would wrap the index and yield duplicate revisions.
	if len(cs.Events) > math.MaxUint16+1 {
		return nil, fmt.Errorf("jetstream: stored changeset exceeds maximum batch size %d", math.MaxUint16+1)
	}

	var result []we.RecordedEvent
	ts := ulid.Timestamp(metadata.Timestamp)
	timestamp := we.TimestampFromTime(metadata.Timestamp)

	for i, event := range cs.Events {
		revision, err := internal.EncodeRevision(ts, metadata.Sequence.Stream, uint16(i))
		if err != nil {
			return nil, err
		}

		recorded := we.RecordedEvent{
			AggregateId: event.AggregateId,
			EventID:     event.EventID,
			Revision:    revision,
			Timestamp:   timestamp,
			EventType:   event.EventType,
			Data:        event.Data,
			Metadata:    event.Metadata,
		}

		result = append(result, recorded)
	}

	return result, nil
}
