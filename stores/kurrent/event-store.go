package kurrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"

	"github.com/weegigs/wee-events-go/we"
)

type EventStoreOption func(*KurrentEventStore)

const defaultPageSize = 97

func PageSize(size int) EventStoreOption {
	return func(es *KurrentEventStore) {
		if size <= 0 {
			size = defaultPageSize
		}

		es.pageSize = size
	}
}

func NewEventStore(client *kurrentdb.Client, options ...EventStoreOption) *KurrentEventStore {
	store := &KurrentEventStore{
		db:       client,
		pageSize: defaultPageSize,
	}

	for _, option := range options {
		option(store)
	}

	return store
}

type KurrentEventStore struct {
	db       *kurrentdb.Client
	pageSize int
}

func (es *KurrentEventStore) Publish(ctx context.Context, aggregateId we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	streamId := aggregateId.Encode().String()
	metadata := map[string]string{}
	if options.CorrelationId != "" {
		metadata["$correlationId"] = options.CorrelationId.String()
	}
	if options.CausationId != "" {
		metadata["$causationId"] = options.CausationId.String()
	}

	var err error
	var md []byte
	if len(metadata) > 0 {
		md, err = json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	esevents := make([]kurrentdb.EventData, len(events))
	for i, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("failed to marshal event: %w", err)
		}

		esevents[i] = kurrentdb.EventData{
			ContentType: kurrentdb.ContentTypeJson,
			EventType:   we.EventTypeOf(event).String(),
			Data:        data,
			Metadata:    md,
		}
	}

	var state kurrentdb.StreamState = kurrentdb.Any{}
	if options.ExpectedRevision == we.InitialRevision {
		state = kurrentdb.NoStream{}
	} else if options.ExpectedRevision != "" {
		// Revisions are encoded as hex on the read path (%026x of the event
		// number), so they must be decoded as base 16 — a base-10 decode breaks
		// for any revision >= 10 (0x0a), where the a-f digits are not valid
		// decimal.
		r, err := strconv.ParseUint(options.ExpectedRevision.String(), 16, 64)
		if err != nil {
			return fmt.Errorf("invalid expected revision: %w", err)
		}
		// KAO - revisions are incremented by one when emitted, so the lowest
		// valid non-initial revision is 1. A value of 0 would underflow the
		// uint64 below and never maps to a real KurrentDB stream revision.
		if r == 0 {
			return errors.New("expected revision must be >= 1")
		}
		r = r - 1

		state = kurrentdb.Revision(r)
	}

	appendOptions := kurrentdb.AppendToStreamOptions{
		StreamState: state,
	}

	_, err = es.db.AppendToStream(ctx, streamId, appendOptions, esevents...)
	if err != nil {
		var kErr *kurrentdb.Error
		if errors.As(err, &kErr) && kErr.Code() == kurrentdb.ErrorCodeWrongExpectedVersion {
			return we.RevisionConflict
		}

		return fmt.Errorf("failed to append to stream: %w", err)
	}

	return nil
}

func (es *KurrentEventStore) Load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	var events []we.RecordedEvent

	var position kurrentdb.StreamPosition = kurrentdb.Start{}
	for {
		page, last, err := es.read(ctx, id, position)
		if err != nil {
			return we.Aggregate{}, err
		}
		events = append(events, page...)
		if (len(page) < int(es.pageSize)) || (len(page) == 0) {
			break
		}

		position = last
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

func (es *KurrentEventStore) read(ctx context.Context, aggregate we.AggregateId, from kurrentdb.StreamPosition) ([]we.RecordedEvent, kurrentdb.StreamPosition, error) {
	if revision, ok := from.(kurrentdb.StreamRevision); ok {
		from = kurrentdb.StreamRevision{
			Value: revision.Value + 1,
		}
	}

	streamId := aggregate.Encode().String()
	stream, err := es.db.ReadStream(
		ctx, streamId, kurrentdb.ReadStreamOptions{
			From: from,
		}, uint64(es.pageSize),
	)
	if err != nil {
		return nil, kurrentdb.End{}, fmt.Errorf("failed to read stream: %w", err)
	}
	defer stream.Close()

	var events []we.RecordedEvent
	var last kurrentdb.StreamPosition

	// KAO: Notes for the future: Read in batches, so I can parallelize the unmarshalling
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			// The KurrentDB client surfaces a missing stream on the first Recv
			// as ErrorCodeResourceNotFound (the legacy ErrStreamNotFound
			// sentinel was removed). An absent stream is the initial state, not
			// a failure, so it loads as an empty aggregate.
			var kErr *kurrentdb.Error
			if errors.As(err, &kErr) && kErr.Code() == kurrentdb.ErrorCodeResourceNotFound {
				return nil, kurrentdb.End{}, nil
			}

			return nil, kurrentdb.End{}, fmt.Errorf("failed to read event: %w", err)
		}

		e := event.OriginalEvent()
		// KAO - the first event in an es stream is event number 0, 0 would translate to initial revision,
		// so I'm incrementing by one to get a usable revision.
		// It *may* be possible to convert this to a ulid of sorts depending on the order of the CreatedDate
		revision := we.Revision(fmt.Sprintf("%026x", e.EventNumber+1))

		var userMetadata map[string]string
		if len(e.UserMetadata) > 0 {
			if err := json.Unmarshal(e.UserMetadata, &userMetadata); err != nil {
				return nil, kurrentdb.End{}, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		metadata := we.RecordedEventMetadata{
			CorrelationId: we.CorrelationID(userMetadata["$correlationId"]),
			CausationId:   we.EventID(userMetadata["$causationId"]),
		}

		recorded := we.RecordedEvent{
			AggregateId: aggregate,
			EventID:     we.EventID(e.EventID.String()),
			Revision:    revision,
			Timestamp:   we.TimestampFromTime(e.CreatedDate),
			EventType:   we.EventType(e.EventType),
			Data: we.Data{
				Encoding: e.ContentType,
				Data:     e.Data,
			},
			Metadata: metadata,
		}

		events = append(events, recorded)

		last = kurrentdb.Revision(e.EventNumber)
	}

	return events, last, nil
}
