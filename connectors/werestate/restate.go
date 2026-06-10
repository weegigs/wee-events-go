// Package werestate exposes a we.EntityService[T] through Restate's durable
// runtime. It mirrors connectors/wehttp: a `load` handler reads current state
// and an `execute` handler applies a {command, payload} envelope, but targets
// Restate's at-most-once, replay-safe execution rather than plain REST.
//
// The connector is built from an already-constructed we.EntityService[T] and
// registers a Restate virtual object keyed by the aggregate id (`type:key`).
// Every command is routed through the service's existing RoutedDispatcher[T];
// no core we/ type is modified and no per-command glue is generated (see
// documents/adr/0004-restate-go-sdk.md).
//
// Addressing: the virtual-object key IS the canonical encoded form produced by
// we.AggregateId.Encode() — `type:key` (IDENTITY-S3.R7, ADR-0010). The
// connector delegates all encoding and decoding to the canonical codec; no
// local separator logic is maintained here.
package werestate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"

	restate "github.com/restatedev/sdk-go"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/weegigs/wee-events-go/we"
)

// EntityResponse is the connector's response envelope. It mirrors the wehttp
// resource encoder: the entity state is flattened into the JSON object and the
// metadata is carried under the $-prefixed keys $id, $type and $revision
// (RESTATE-S1.R2, RESTATE-S1.R3, RESTATE-S1.R4).
type EntityResponse struct {
	State    map[string]any
	ID       we.EncodedAggregateId
	Type     we.EntityType
	Revision we.Revision
}

// MarshalJSON renders the response with the same shape as the wehttp encoder:
// the state fields at the top level alongside $id/$type/$revision.
func (r EntityResponse) MarshalJSON() ([]byte, error) {
	resource := make(map[string]any, len(r.State)+3)
	maps.Copy(resource, r.State)
	resource["$id"] = r.ID
	resource["$type"] = r.Type
	resource["$revision"] = r.Revision

	return json.Marshal(resource)
}

// UnmarshalJSON is the inverse of MarshalJSON. It is required because the
// connector's execute handler returns the response through restate.Run, whose
// journal round-trips the value (marshal on first run, unmarshal on replay); an
// asymmetric codec would lose the flattened state on replay.
func (r *EntityResponse) UnmarshalJSON(data []byte) error {
	resource := make(map[string]any)
	if err := json.Unmarshal(data, &resource); err != nil {
		return err
	}

	id, ok := resource["$id"].(string)
	if !ok {
		return fmt.Errorf("werestate: entity response missing or non-string $id (got %T)", resource["$id"])
	}
	typ, ok := resource["$type"].(string)
	if !ok {
		return fmt.Errorf("werestate: entity response missing or non-string $type (got %T)", resource["$type"])
	}
	revision, ok := resource["$revision"].(string)
	if !ok {
		return fmt.Errorf("werestate: entity response missing or non-string $revision (got %T)", resource["$revision"])
	}
	r.ID = we.EncodedAggregateId(id)
	r.Type = we.EntityType(typ)
	r.Revision = we.Revision(revision)

	delete(resource, "$id")
	delete(resource, "$type")
	delete(resource, "$revision")
	r.State = resource

	return nil
}

// Option configures a service before its handlers are registered.
type Option[T any] func(*service[T])

// Logger sets the logger used for info/debug output.
func Logger[T any](logger *zerolog.Logger) Option[T] {
	return func(s *service[T]) {
		s.log = logger
	}
}

// Serializer overrides how entity state is projected into the response object.
// It mirrors wehttp's custom resource encoder.
func Serializer[T any](serializer we.EntitySerializer[T]) Option[T] {
	return func(s *service[T]) {
		s.serializer = serializer
	}
}

// service adapts a we.EntityService[T] to Restate handlers.
type service[T any] struct {
	log        *zerolog.Logger
	controller we.EntityService[T]
	serializer we.EntitySerializer[T]
}

// NewService builds a Restate connector over an existing we.EntityService[T].
// The connector owns no domain wiring; it is handed a fully constructed service
// and registers its handlers explicitly (principle 2, RESTATE-S1.R1).
func NewService[T any](controller we.EntityService[T], options ...Option[T]) *service[T] {
	s := &service[T]{controller: controller, serializer: we.StateSerializer[T]}
	for _, option := range options {
		option(s)
	}
	if s.log == nil {
		s.log = &log.Logger
	}
	// Serializer(nil) resets to the default state serializer rather than
	// leaving a nil projector that would panic at request time.
	if s.serializer == nil {
		s.serializer = we.StateSerializer[T]
	}

	return s
}

// Definition registers a Restate virtual object, keyed by the aggregate id
// (`type:key`), exposing `load` and `execute` handlers for the supplied service
// (RESTATE-S1.R1, RESTATE-S4.R2 — only these two handlers; no effect routing).
func (s *service[T]) Definition(name string) restate.ServiceDefinition {
	return restate.NewObject(name).
		Handler("load", restate.NewObjectSharedHandler(s.loadHandler())).
		Handler("execute", restate.NewObjectHandler(s.executeHandler()))
}

// loadHandler is the Restate shared handler for `load`. The virtual object key
// is the encoded aggregate id; the handler decodes it and returns current state
// (RESTATE-S1.R2).
func (s *service[T]) loadHandler() restate.ObjectSharedHandlerFn[restate.Void, EntityResponse] {
	return func(ctx restate.ObjectSharedContext, _ restate.Void) (EntityResponse, error) {
		id, err := decodeKey(restate.Key(ctx))
		if err != nil {
			return EntityResponse{}, restate.TerminalError(err, http.StatusBadRequest)
		}

		response, err := s.load(ctx, id)
		if err != nil {
			s.log.Info().Err(err).Str("type", id.Type).Str("key", id.Key).Msg("failed to load entity")
			return EntityResponse{}, mapError(err)
		}

		return response, nil
	}
}

// executeHandler is the Restate exclusive handler for `execute`. The dispatch is
// wrapped in restate.Run so the runtime journals the outcome: on a restart or a
// replayed invocation carrying the same idempotency key, the journaled result is
// yielded instead of re-applying the command (RESTATE-S2.R1–R4). Restate's
// exclusive-per-key semantics serialise concurrent executes for one aggregate.
func (s *service[T]) executeHandler() restate.ObjectHandlerFn[we.RemoteCommand, EntityResponse] {
	return func(ctx restate.ObjectContext, command we.RemoteCommand) (EntityResponse, error) {
		id, err := decodeKey(restate.Key(ctx))
		if err != nil {
			return EntityResponse{}, restate.TerminalError(err, http.StatusBadRequest)
		}

		response, err := restate.Run(ctx, func(runCtx restate.RunContext) (EntityResponse, error) {
			result, execErr := s.execute(runCtx, id, command)
			if execErr != nil {
				return EntityResponse{}, mapError(execErr)
			}
			return result, nil
		})
		if err != nil {
			s.log.Info().Err(err).Str("type", id.Type).Str("key", id.Key).
				Str("command", string(command.CommandName)).Msg("failed to execute command")
			return EntityResponse{}, err
		}

		return response, nil
	}
}

// execute dispatches the command through the supplied EntityService[T] (which
// owns the RoutedDispatcher[T]) and projects the resulting entity. This is the
// transport-agnostic core, unit-tested without a Restate context (RESTATE-S1.R3,
// RESTATE-S1.R5).
func (s *service[T]) execute(ctx context.Context, id we.AggregateId, command we.RemoteCommand) (EntityResponse, error) {
	entity, err := s.controller.Execute(ctx, id, command)
	if err != nil {
		return EntityResponse{}, err
	}

	return s.project(entity)
}

// load reads current entity state through the supplied EntityService[T]
// (RESTATE-S1.R2).
func (s *service[T]) load(ctx context.Context, id we.AggregateId) (EntityResponse, error) {
	entity, err := s.controller.Load(ctx, id)
	if err != nil {
		return EntityResponse{}, err
	}

	return s.project(entity)
}

func (s *service[T]) project(entity we.Entity[T]) (EntityResponse, error) {
	state, err := s.serializer(entity)
	if err != nil {
		return EntityResponse{}, err
	}

	return EntityResponse{
		State:    state,
		ID:       entity.Aggregate.Encode(),
		Type:     entity.Type,
		Revision: entity.Revision,
	}, nil
}

// EncodeKey builds the virtual-object key for an aggregate id. The Restate
// object key IS the canonical encoded form (IDENTITY-S3.R7, ADR-0010): one
// codec, one set of invariants, shared with every other identity edge.
func EncodeKey(id we.AggregateId) (string, error) {
	valid, err := we.MakeAggregateId(id.Type, id.Key)
	if err != nil {
		return "", err
	}
	return valid.Encode().String(), nil
}

// decodeKey delegates to the canonical parser; the handlers classify its
// failures as terminal bad-request errors.
func decodeKey(key string) (we.AggregateId, error) {
	return we.EncodedAggregateId(key).Decode()
}

// mapError applies the connector's boundary error mapping (RESTATE-S3),
// classifying a handler error as a Restate TERMINAL error (the runtime stops and
// returns it) or leaving it RETRYABLE (the runtime retries). The rule, using the
// Feature 05 rejection taxonomy:
//
//   - we.Rejection (recovered via errors.As) — a domain refusal of a well-formed
//     command. TERMINAL: retrying a refused command cannot change the outcome.
//     The rejection value is the wrapped error, so its code/message/context stay
//     recoverable through the terminal error's Unwrap chain (RESTATE-S3.R2,
//     RESTATE-S3.R3).
//   - *we.DecodeError (recovered via errors.As) — an inbound command payload that
//     declared an unsupported encoding or carried malformed bytes. TERMINAL: the
//     bytes are deterministically bad, so retrying loops forever on a poison
//     message. This subsumes the bare *we.InvalidEncodingError, which now arrives
//     wrapped as a *we.DecodeError from HandleRemoteCommand.
//   - we.CommandNotFoundError (recovered via errors.As) — an unknown command
//     name. TERMINAL: the name will not become known on retry.
//   - Everything else — store failures, transport failures, and the
//     we.RevisionConflict optimistic-concurrency sentinel — is treated as
//     infrastructure and left RETRYABLE so transient faults self-heal
//     (RESTATE-S3.R1). RevisionConflict is explicitly NOT a rejection; it is a
//     retry signal (ADR-0005).
//
// An error already marked terminal is returned unchanged.
func mapError(err error) error {
	if err == nil {
		return nil
	}

	if restate.IsTerminalError(err) {
		return err
	}

	var rejection we.Rejection
	if errors.As(err, &rejection) {
		return restate.TerminalError(err, http.StatusUnprocessableEntity)
	}

	var decode *we.DecodeError
	if errors.As(err, &decode) {
		return restate.TerminalError(err, http.StatusBadRequest)
	}

	var notFound we.CommandNotFoundError
	if errors.As(err, &notFound) {
		return restate.TerminalError(err, http.StatusBadRequest)
	}

	// Infrastructure / unclassified (store, transport, RevisionConflict): retryable.
	return err
}
