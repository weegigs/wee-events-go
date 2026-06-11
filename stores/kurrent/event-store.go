package kurrent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
	"github.com/rs/zerolog/log"

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

// NewEventStore builds a we.EventStore over KurrentDB. The store constructs
// and owns its kurrentdb.Client: the v1.2.0 client poisons itself permanently
// once rediscovery exhausts MaxDiscoverAttempts (the connection state-machine
// goroutine sets a one-way close flag and exits, kurrentdb/impl.go:240-247;
// see documents/roadmap.md), so the store retains the settings to rebuild a
// fresh client transparently when that happens. The retained Configuration
// is owned by the store from this point on: callers must not mutate it after
// construction. The encoder is the store's explicit write encoding
// (ENCODING-S2.R1); nil is a construction error, never a deferred panic
// (ENCODING-S2.R2).
func NewEventStore(settings *kurrentdb.Configuration, encoder we.Encoder, options ...EventStoreOption) (*KurrentEventStore, error) {
	if encoder == nil {
		return nil, errors.New("kurrent: encoder is required")
	}
	if settings == nil {
		return nil, errors.New("kurrent: settings are required")
	}

	client, err := kurrentdb.NewClient(settings)
	if err != nil {
		return nil, fmt.Errorf("kurrent: failed to create client: %w", err)
	}

	store := &KurrentEventStore{
		settings: settings,
		db:       client,
		pageSize: defaultPageSize,
		encoder:  encoder,
	}

	for _, option := range options {
		option(store)
	}

	return store, nil
}

// StoreClosed reports an operation on a store after Close. Named in the
// repo's sentinel style (we.RevisionConflict, we.NilEncoder).
var StoreClosed = errors.New("kurrent: store is closed")

type KurrentEventStore struct {
	settings *kurrentdb.Configuration
	mu       sync.RWMutex
	db       *kurrentdb.Client
	closed   bool
	pageSize int
	encoder  we.Encoder
}

// Close releases the store's current client. The store constructs its own
// clients, so closing the store is the only way callers release the
// underlying gRPC channel. Close is TERMINAL: it marks the store closed so
// the recovery path cannot resurrect it — an operation racing or following
// Close sees the client's ErrorCodeConnectionClosed, reaches rebuild, and is
// refused with StoreClosed instead of swapping in a client nobody would
// close. A second Close is a no-op state, not an error.
func (es *KurrentEventStore) Close() error {
	es.mu.Lock()
	defer es.mu.Unlock()
	if es.closed {
		return nil
	}
	es.closed = true
	return es.db.Close()
}

// client returns the current client under the read lock so operations never
// observe a half-swapped rebuild.
func (es *KurrentEventStore) client() *kurrentdb.Client {
	es.mu.RLock()
	defer es.mu.RUnlock()
	return es.db
}

// connectionClosed classifies the kurrentdb client's permanent poison state:
// once rediscovery exhausts MaxDiscoverAttempts the client returns
// ErrorCodeConnectionClosed from every operation, forever.
func connectionClosed(err error) bool {
	var kErr *kurrentdb.Error
	return errors.As(err, &kErr) && kErr.Code() == kurrentdb.ErrorCodeConnectionClosed
}

// rebuild swaps a fresh client in for a poisoned one. The kurrentdb v1.2.0
// client has no reset API — kurrentdb.NewClient is the only recovery (see
// documents/roadmap.md) — so the store rebuilds from its retained settings.
// Single-flight under concurrency: rebuild only happens when the current
// client is still the one the caller saw fail; a goroutine arriving after
// another already swapped reuses the new client instead of rebuilding again.
func (es *KurrentEventStore) rebuild(failed *kurrentdb.Client) (*kurrentdb.Client, error) {
	es.mu.Lock()
	defer es.mu.Unlock()

	if es.closed {
		// Close is terminal: a closed store's client fails with
		// ErrorCodeConnectionClosed and lands here, but rebuilding would
		// resurrect the store with a client nobody will close.
		return nil, StoreClosed
	}

	if es.db != failed {
		return es.db, nil
	}

	fresh, err := kurrentdb.NewClient(es.settings)
	if err != nil {
		// The poisoned client stays in place: the next operation will fail
		// with ErrorCodeConnectionClosed and attempt the rebuild again.
		return nil, fmt.Errorf("failed to rebuild kurrentdb client: %w", err)
	}

	// Best-effort release of the poisoned client before discarding it; its
	// state machine has already exited, so a close error is expected noise,
	// not a reason to fail the operation.
	if cerr := failed.Close(); cerr != nil {
		log.Debug().Err(cerr).Msg("kurrent: closing poisoned client")
	}

	es.db = fresh
	return fresh, nil
}

func (es *KurrentEventStore) Publish(ctx context.Context, aggregateId we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	if len(events) == 0 {
		// An empty publish is a no-op, not an error (CONFORMANCE-S3). Guarding
		// here matches the other stores and avoids a pointless append round-trip
		// whose outcome would otherwise depend on KurrentDB's server-side
		// handling of a zero-event append. The no-op deliberately short-circuits
		// BEFORE override validation: with zero events the encoder is never used
		// and nothing can be mis-recorded, so an empty publish is a state, not
		// an error.
		return nil
	}

	// Resolve the publish encoder before encoding: an explicit per-publish
	// override wins, and an explicit nil override is an error that records
	// nothing (ENCODING-S2.R3, ENCODING-S2.R5).
	encoder, err := options.EncoderFor(es.encoder)
	if err != nil {
		return fmt.Errorf("kurrent: %w", err)
	}

	encoded := make([]we.Data, len(events))
	for i, event := range events {
		data, err := we.MarshalToData(encoder, event)
		if err != nil {
			return fmt.Errorf("failed to encode event: %w", err)
		}
		encoded[i] = data
	}

	streamId := aggregateId.Encode().String()
	metadata := map[string]string{}
	if options.CorrelationId != "" {
		metadata["$correlationId"] = options.CorrelationId.String()
	}
	if options.CausationId != "" {
		metadata["$causationId"] = options.CausationId.String()
	}
	// Persist the encoding discriminator: KurrentDB's ContentType only
	// distinguishes JSON from binary, so the true discriminator (e.g.
	// application/cbor) must travel in the user metadata. The encoder is fixed
	// per publish, so one entry describes the whole batch. This also makes the
	// user metadata ALWAYS non-empty — previously events without correlation or
	// causation ids stored no metadata at all — so external stream consumers
	// using metadata-presence checks see different behavior for new events.
	metadata["$encoding"] = encoded[0].Encoding

	md, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	esevents := make([]kurrentdb.EventData, len(events))
	for i, event := range events {
		contentType := kurrentdb.ContentTypeBinary
		if encoded[i].Encoding == we.JSONEncoding {
			contentType = kurrentdb.ContentTypeJson
		}

		esevents[i] = kurrentdb.EventData{
			ContentType: contentType,
			EventType:   we.EventTypeOf(event).String(),
			Data:        encoded[i].Data,
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

	client := es.client()
	_, err = client.AppendToStream(ctx, streamId, appendOptions, esevents...)
	if connectionClosed(err) {
		// The client has poisoned itself permanently (one-way close flag, no
		// reset API — documents/roadmap.md); rebuild so the store heals even
		// when the append below is not retried.
		fresh, rerr := es.rebuild(client)
		if rerr != nil {
			return rerr
		}
		// Retry ONLY when the append names an exact stream expectation
		// (NoStream or a specific revision). ErrorCodeConnectionClosed is not
		// always pre-dispatch: in the poisoned steady state
		// getConnectionHandle refuses before the request leaves the process
		// (kurrentdb v1.2.0 impl.go:104-110), but handleError
		// (impl.go:37-43) also maps a POST-dispatch RPC failure to
		// ErrorCodeConnectionClosed when the close flag is set concurrently —
		// so the first append may have committed with only its response lost.
		// Under an exact expectation a duplicate retry surfaces as
		// WrongExpectedVersion and is returned as we.RevisionConflict below:
		// an honest, caller-visible error, never a silent duplicate. Under
		// Any{} a duplicate retry would succeed silently, so the original
		// error is returned instead and the NEXT call succeeds on the rebuilt
		// client. A failed retry is returned as-is: recovery must never mask
		// a still-down server.
		if _, blind := state.(kurrentdb.Any); !blind {
			_, err = fresh.AppendToStream(ctx, streamId, appendOptions, esevents...)
		}
	}
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

// read fetches one page, recovering from a poisoned client the same way the
// write path does: the client poisons itself permanently once rediscovery
// exhausts MaxDiscoverAttempts (documents/roadmap.md), so the page is retried
// exactly once on a rebuilt client. Retrying the whole page is safe — reads
// are idempotent. A second failure is returned as-is.
func (es *KurrentEventStore) read(ctx context.Context, aggregate we.AggregateId, from kurrentdb.StreamPosition) ([]we.RecordedEvent, kurrentdb.StreamPosition, error) {
	client := es.client()
	events, last, err := es.readPage(ctx, client, aggregate, from)
	if !connectionClosed(err) {
		return events, last, err
	}

	fresh, rerr := es.rebuild(client)
	if rerr != nil {
		return nil, kurrentdb.End{}, rerr
	}

	return es.readPage(ctx, fresh, aggregate, from)
}

func (es *KurrentEventStore) readPage(ctx context.Context, client *kurrentdb.Client, aggregate we.AggregateId, from kurrentdb.StreamPosition) ([]we.RecordedEvent, kurrentdb.StreamPosition, error) {
	if revision, ok := from.(kurrentdb.StreamRevision); ok {
		from = kurrentdb.StreamRevision{
			Value: revision.Value + 1,
		}
	}

	streamId := aggregate.Encode().String()
	stream, err := client.ReadStream(
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

		// $encoding stays out of RecordedEventMetadata: only the named
		// correlation and causation keys map to user-visible fields.
		metadata := we.RecordedEventMetadata{
			CorrelationId: we.CorrelationID(userMetadata["$correlationId"]),
			CausationId:   we.EventID(userMetadata["$causationId"]),
		}

		// The persisted $encoding discriminator is authoritative. Events stored
		// before the discriminator existed carry none: for those the KurrentDB
		// ContentType *is* the recorded discriminator (the JSON-era write path
		// always set ContentTypeJson), so falling through to it is a read of
		// stored state, not a default.
		encoding := userMetadata["$encoding"]
		if encoding == "" {
			encoding = e.ContentType
		}

		recorded := we.RecordedEvent{
			AggregateId: aggregate,
			EventID:     we.EventID(e.EventID.String()),
			Revision:    revision,
			Timestamp:   we.TimestampFromTime(e.CreatedDate),
			EventType:   we.EventType(e.EventType),
			Data: we.Data{
				Encoding: encoding,
				Data:     e.Data,
			},
			Metadata: metadata,
		}

		events = append(events, recorded)

		last = kurrentdb.Revision(e.EventNumber)
	}

	return events, last, nil
}
