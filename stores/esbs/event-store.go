package esdbs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/EventStore/EventStore-Client-Go/esdb"
	"github.com/pkg/errors"

	"github.com/weegigs/wee-events-go/we"
)

type EventStoreOption func(*ESDBEventStore)

const defaultPageSize = 97

func PageSize(size int) EventStoreOption {
	return func(es *ESDBEventStore) {
		if size <= 0 {
			size = defaultPageSize
		}

		es.pageSize = size
	}
}

func NewEventStore(client *esdb.Client, options ...EventStoreOption) *ESDBEventStore {
	store := &ESDBEventStore{
		db:       client,
		pageSize: defaultPageSize,
	}

	for _, option := range options {
		option(store)
	}

	return store
}

type ESDBEventStore struct {
	db       *esdb.Client
	pageSize int
}

func (es *ESDBEventStore) Publish(ctx context.Context, aggregateId we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	streamId := aggregateId.Encode().String()
	metadata := map[string]string{}
	if options.RecordedEventMetadata.CorrelationId != "" {
		metadata["$correlationId"] = options.RecordedEventMetadata.CorrelationId.String()
	}
	if options.RecordedEventMetadata.CausationId != "" {
		metadata["$causationId"] = options.RecordedEventMetadata.CausationId.String()
	}

	var err error
	var md []byte
	if len(metadata) > 0 {
		md, err = json.Marshal(metadata)
		if err != nil {
			return errors.Wrap(err, "failed to marshal metadata")
		}
	}

	esevents := make([]esdb.EventData, len(events))
	for i, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return errors.Wrap(err, "failed to marshal event")
		}

		esevents[i] = esdb.EventData{
			ContentType: esdb.JsonContentType,
			EventType:   we.EventTypeOf(event).String(),
			Data:        data,
			Metadata:    md,
		}
	}

	var revision esdb.ExpectedRevision = esdb.Any{}
	if options.ExpectedRevision == we.InitialRevision {
		revision = esdb.NoStream{}
	} else if options.ExpectedRevision != "" {
		r, err := strconv.ParseUint(options.ExpectedRevision.String(), 10, 64)
		if err != nil {
			return errors.Wrap(err, "invalid expected revision")
		}
		r = r - 1 // KAO - revisions are incremented by one when emitted
		if r < 0 {
			return errors.New("invalid expected revision")
		}

		revision = esdb.Revision(r)
	}

	esdbOptions := esdb.AppendToStreamOptions{
		ExpectedRevision: revision,
	}

	_, err = es.db.AppendToStream(ctx, streamId, esdbOptions, esevents...)
	if err != nil {
		if err == esdb.ErrWrongExpectedStreamRevision {
			return we.RevisionConflict
		}

		return errors.Wrap(err, "failed to append to stream")
	}

	return nil
}

func (es *ESDBEventStore) Load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	var events []we.RecordedEvent

	var position esdb.StreamPosition = esdb.Start{}
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

func (es *ESDBEventStore) read(ctx context.Context, aggregate we.AggregateId, from esdb.StreamPosition) ([]we.RecordedEvent, esdb.StreamPosition, error) {
	if revision, ok := from.(esdb.StreamRevision); ok {
		from = esdb.StreamRevision{
			Value: revision.Value + 1,
		}
	}

	streamId := aggregate.Encode().String()
	stream, err := es.db.ReadStream(
		ctx, streamId, esdb.ReadStreamOptions{
			From: from,
		}, uint64(es.pageSize),
	)
	if err != nil {
		if err == esdb.ErrStreamNotFound {
			return nil, esdb.End{}, nil
		}

		if errors.Is(err, io.EOF) {
			return nil, esdb.End{}, nil
		}

		return nil, esdb.End{}, errors.Wrap(err, "failed to read stream")
	}
	defer stream.Close()

	var events []we.RecordedEvent
	var last esdb.StreamPosition

	// KAO: Notes for the future: Read in batches, so I can parallelize the unmarshalling
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, esdb.End{}, errors.Wrap(err, "failed to read event")
		}

		e := event.OriginalEvent()
		// KAO - the first event in an es stream is event number 0, 0 would translate to initial revision,
		// so I'm incrementing by one to get a usable revision.
		// It *may* be possible to convert this to a ulid of sorts depending on the order of the CreatedDate
		revision := we.Revision(fmt.Sprintf("%026x", e.EventNumber+1))

		var userMetadata map[string]string
		if len(e.UserMetadata) > 0 {
			if err := json.Unmarshal(e.UserMetadata, &userMetadata); err != nil {
				return nil, esdb.End{}, errors.Wrap(err, "failed to unmarshal metadata")
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

		last = esdb.Revision(e.EventNumber)
	}

	return events, last, nil
}
