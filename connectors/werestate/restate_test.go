package werestate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	restate "github.com/restatedev/sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/samples/counter"
	"github.com/weegigs/wee-events-go/we"
)

// memoryStore is a minimal in-process EventStore used to wire the counter
// sample through the connector without a containerised backend. It supports the
// connector's unit tests for envelope shape, dispatch routing, and load.
type memoryStore struct {
	mu        sync.Mutex
	events    map[we.AggregateId][]we.RecordedEvent
	revisions *we.RevisionGenerator
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		events:    make(map[we.AggregateId][]we.RecordedEvent),
		revisions: we.NewRevisionGenerator(),
	}
}

func (s *memoryStore) Load(_ context.Context, id we.AggregateId) (we.Aggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	events := s.events[id]
	revision := we.InitialRevision
	if len(events) > 0 {
		revision = events[len(events)-1].Revision
	}

	return we.Aggregate{Id: id, Events: events, Revision: revision}, nil
}

func (s *memoryStore) Publish(_ context.Context, id we.AggregateId, _ we.PublishOptions, events ...we.DomainEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.events[id]
	for _, event := range events {
		data, err := we.MarshalToData(event)
		if err != nil {
			return err
		}
		revision := s.revisions.NewRevision(time.Now())
		recorded := we.RecordedEvent{
			AggregateId: id,
			Revision:    revision,
			EventID:     we.EventID(revision.String()),
			EventType:   we.EventTypeOf(event),
			Data:        data,
		}
		existing = append(existing, recorded)
	}
	s.events[id] = existing

	return nil
}

// failingStore fails on Load to exercise infrastructure error mapping.
type failingStore struct{ err error }

func (s *failingStore) Load(context.Context, we.AggregateId) (we.Aggregate, error) {
	return we.Aggregate{}, s.err
}

func (s *failingStore) Publish(context.Context, we.AggregateId, we.PublishOptions, ...we.DomainEvent) error {
	return s.err
}

func counterService(store we.EventStore) we.EntityService[counter.Counter] {
	loader := counter.Loader(store)
	dispatcher := we.RoutedDispatcher[counter.Counter]{
		Handlers: counter.CommandHandlers(func() int { return 7 }),
		Publish:  store.Publish,
	}
	return we.NewEntityService(loader, &dispatcher)
}

func incrementEnvelope(t *testing.T, amount int) we.RemoteCommand {
	t.Helper()
	payload, err := json.Marshal(counter.Increment{Amount: amount})
	require.NoError(t, err)
	return we.RemoteCommand{
		CommandName: counter.IncrementCmd,
		Payload:     we.Data{Encoding: "application/json", Data: payload},
	}
}

// RESTATE-S1.R5 — dispatch goes through the supplied EntityService[T] /
// RoutedDispatcher[T]; the connector does not re-implement command routing.
func TestExecuteRoutesThroughEntityService(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(counterService(store))

	id := we.AggregateId{Type: "counter", Key: "abc"}
	response, err := svc.execute(context.Background(), id, incrementEnvelope(t, 5))
	require.NoError(t, err)

	assert.Equal(t, we.AggregateId{Type: "counter", Key: "abc"}.Encode(), response.ID)
	assert.EqualValues(t, "counter:counter", response.Type)
	assert.Equal(t, float64(5), response.State["current"])
}

// RESTATE-S1.R2 — load returns current state with $id/$type/$revision.
func TestLoadReturnsStateAndMetadata(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(counterService(store))

	id := we.AggregateId{Type: "counter", Key: "load-1"}
	_, err := svc.execute(context.Background(), id, incrementEnvelope(t, 3))
	require.NoError(t, err)

	loaded, err := svc.load(context.Background(), id)
	require.NoError(t, err)

	assert.Equal(t, id.Encode(), loaded.ID)
	assert.EqualValues(t, "counter:counter", loaded.Type)
	assert.Equal(t, float64(3), loaded.State["current"])
	assert.NotEmpty(t, loaded.Revision)
}

// RESTATE-S1.R4 — the request/response envelope matches wehttp:
// request {command, payload:{encoding, data}}; response carries $id/$type/$revision.
func TestEnvelopeMatchesHTTPShape(t *testing.T) {
	// Request envelope decodes from the same JSON wehttp accepts.
	body := []byte(`{"command":"counter:increment","payload":{"encoding":"application/json","data":{"Amount":4}}}`)
	var command we.RemoteCommand
	require.NoError(t, json.Unmarshal(body, &command))
	assert.EqualValues(t, "counter:increment", command.CommandName)
	assert.Equal(t, "application/json", command.Payload.Encoding)

	store := newMemoryStore()
	svc := NewService(counterService(store))
	id := we.AggregateId{Type: "counter", Key: "env-1"}

	response, err := svc.execute(context.Background(), id, command)
	require.NoError(t, err)

	// Response serialises with the $-prefixed metadata keys, like the HTTP encoder.
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Contains(t, decoded, "$id")
	assert.Contains(t, decoded, "$type")
	assert.Contains(t, decoded, "$revision")
	assert.Equal(t, float64(4), decoded["current"])
}

// RESTATE-S1.R4 / RESTATE-S2 — the response envelope round-trips losslessly.
// The execute handler returns its result through restate.Run, whose journal
// marshals then unmarshals the value; an asymmetric codec would drop the
// flattened state on replay, breaking the "return the original result" guarantee.
func TestEntityResponseRoundTrips(t *testing.T) {
	original := EntityResponse{
		State:    map[string]any{"current": float64(11)},
		ID:       we.AggregateId{Type: "counter", Key: "rt-1"}.Encode(),
		Type:     "counter:counter",
		Revision: we.Revision("01ARZ3NDEKTSV4RRFFQ69G5FAV"),
	}

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded EntityResponse
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	assert.Equal(t, original.ID, decoded.ID)
	assert.Equal(t, original.Type, decoded.Type)
	assert.Equal(t, original.Revision, decoded.Revision)
	assert.Equal(t, original.State, decoded.State)
}

// RESTATE-S3.R1 — an infrastructure (store/transport) failure surfaces as a
// retryable Restate error so the runtime may retry.
func TestInfrastructureFailureIsRetryable(t *testing.T) {
	boom := errors.New("dynamo unavailable")
	svc := NewService(counterService(&failingStore{err: boom}))

	id := we.AggregateId{Type: "counter", Key: "fail-1"}
	_, err := svc.execute(context.Background(), id, incrementEnvelope(t, 1))
	require.Error(t, err)

	mapped := mapError(err)
	require.Error(t, mapped)
	assert.False(t, restate.IsTerminalError(mapped), "infrastructure error must be retryable, not terminal")
}

// RESTATE-S3.R3 — an unknown command name (CommandNotFoundError) is a refusal
// that retrying cannot resolve, so it maps to a terminal error.
func TestCommandNotFoundIsTerminal(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(counterService(store))

	id := we.AggregateId{Type: "counter", Key: "refuse-1"}
	unknown := we.RemoteCommand{
		CommandName: "counter:teleport",
		Payload:     we.Data{Encoding: "application/json", Data: json.RawMessage(`{}`)},
	}

	_, err := svc.execute(context.Background(), id, unknown)
	require.Error(t, err)
	var notFound we.CommandNotFoundError
	require.True(t, errors.As(err, &notFound), "expected a command-not-found refusal")

	mapped := mapError(err)
	require.Error(t, mapped)
	assert.True(t, restate.IsTerminalError(mapped), "a refusal must be terminal, never retryable")
}

// RESTATE-S3.R2 — a domain rejection maps to a terminal error that carries the
// rejection's code, message, and context (recoverable through the terminal
// error's Unwrap chain via errors.As).
func TestRejectionMapsToTerminalCarryingDetail(t *testing.T) {
	rejection := we.MakeRejection(
		"counter.limit-exceeded",
		"increment would exceed the configured ceiling",
		json.RawMessage(`{"ceiling":100}`),
	)

	mapped := mapError(rejection)
	require.Error(t, mapped)
	require.True(t, restate.IsTerminalError(mapped), "a domain rejection must be terminal")

	var recovered we.Rejection
	require.True(t, errors.As(mapped, &recovered), "rejection must stay recoverable through the terminal error")
	assert.Equal(t, "counter.limit-exceeded", recovered.Code)
	assert.Equal(t, "increment would exceed the configured ceiling", recovered.Message)
	assert.JSONEq(t, `{"ceiling":100}`, string(recovered.Context))
}

// RESTATE-S3.R2 — a rejection wrapped in a larger error chain is still
// recovered via errors.As and mapped terminal.
func TestWrappedRejectionMapsToTerminal(t *testing.T) {
	rejection := we.MakeRejection("counter.refused", "no", nil)
	wrapped := fmt.Errorf("dispatch failed: %w", rejection)

	mapped := mapError(wrapped)
	require.True(t, restate.IsTerminalError(mapped), "a wrapped rejection must still be terminal")
	var recovered we.Rejection
	require.True(t, errors.As(mapped, &recovered))
	assert.Equal(t, "counter.refused", recovered.Code)
}

// RESTATE-S3.R1 / fixes the infinite-retry bug — an inbound command payload that
// cannot be decoded (unsupported encoding OR malformed bytes) arrives from
// HandleRemoteCommand wrapped as a *we.DecodeError. It is a deterministic bad
// input: retrying loops forever on a poison message, so it must be terminal.
func TestDecodeErrorIsTerminal(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(counterService(store))
	id := we.AggregateId{Type: "counter", Key: "decode-1"}

	cases := []struct {
		name    string
		command we.RemoteCommand
	}{
		{
			name: "unsupported encoding",
			command: we.RemoteCommand{
				CommandName: counter.IncrementCmd,
				Payload:     we.Data{Encoding: "application/x-nonsense", Data: json.RawMessage(`{"Amount":1}`)},
			},
		},
		{
			name: "malformed payload",
			command: we.RemoteCommand{
				CommandName: counter.IncrementCmd,
				Payload:     we.Data{Encoding: "application/json", Data: json.RawMessage(`{"Amount":`)},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.execute(context.Background(), id, tc.command)
			require.Error(t, err)
			var decode *we.DecodeError
			require.True(t, errors.As(err, &decode), "inbound decode failure must surface as *we.DecodeError")

			mapped := mapError(err)
			require.Error(t, mapped)
			assert.True(t, restate.IsTerminalError(mapped), "a decode failure must be terminal, never retryable")
		})
	}
}

// RESTATE-S3.R1 / ADR-0005 — the optimistic-concurrency sentinel is NOT a
// rejection; it is an infrastructure retry signal and must stay retryable.
func TestRevisionConflictIsRetryable(t *testing.T) {
	mapped := mapError(we.RevisionConflict)
	require.Error(t, mapped)
	assert.False(t, restate.IsTerminalError(mapped), "RevisionConflict is a retry signal, not terminal")
}

// EncodeKey/decodeKey must form a bijection. A colon in the aggregate Type would
// make the addressing key ambiguous (the first colon is the type:key separator),
// so EncodeKey rejects it at the boundary with a terminal-classified error.
func TestEncodeKeyRejectsColonInType(t *testing.T) {
	t.Run("colon in type is rejected", func(t *testing.T) {
		_, err := EncodeKey(we.AggregateId{Type: "counter:evil", Key: "k1"})
		require.Error(t, err)
		// The handlers wrap this boundary error as a terminal bad-request error.
		assert.True(t, restate.IsTerminalError(mapError(restate.TerminalError(err, http.StatusBadRequest))))
	})

	t.Run("colon in key round-trips", func(t *testing.T) {
		id := we.AggregateId{Type: "counter", Key: "tenant:42"}
		key, err := EncodeKey(id)
		require.NoError(t, err)
		decoded, err := decodeKey(key)
		require.NoError(t, err)
		assert.Equal(t, id, decoded)
	})
}

// EncodeKey must only produce keys decodeKey accepts: an empty Type or Key
// encodes into a string decodeKey rejects, so both are refused at the boundary,
// keeping the documented bijection exact.
func TestEncodeKeyRejectsEmptyFields(t *testing.T) {
	t.Run("empty type is rejected", func(t *testing.T) {
		_, err := EncodeKey(we.AggregateId{Type: "", Key: "k1"})
		require.Error(t, err)
	})

	t.Run("empty key is rejected", func(t *testing.T) {
		_, err := EncodeKey(we.AggregateId{Type: "counter", Key: ""})
		require.Error(t, err)
	})
}

// RESTATE-S4.R2 — phase 1 scope is execute/load/idempotency only; no
// effect-routing surface is exposed by the connector.
func TestPhaseOneScopeHasNoEffectRouting(t *testing.T) {
	store := newMemoryStore()
	svc := NewService(counterService(store))

	definition := svc.Definition("counter")
	handlers := definition.Handlers()

	_, hasExecute := handlers["execute"]
	_, hasLoad := handlers["load"]
	assert.True(t, hasExecute, "execute handler must be registered")
	assert.True(t, hasLoad, "load handler must be registered")
	assert.Len(t, handlers, 2, "phase 1 exposes only execute and load; no effect-routing handlers")
}
