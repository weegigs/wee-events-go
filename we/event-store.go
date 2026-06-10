package we

import (
	"context"
	"errors"
)

type EventLoader = func(ctx context.Context, id AggregateId) (Aggregate, error)
type EventPublisher = func(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) error

type EventStore interface {
	Load(ctx context.Context, id AggregateId) (Aggregate, error)
	Publish(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) error
}

func Loader(store EventStore) EventLoader {
	return store.Load
}

func Publisher(store EventStore) EventPublisher {
	return store.Publish
}

var RevisionConflict = errors.New("revision-conflict")

var NilEncoder = errors.New("encoder must not be nil")

type PublishOptions struct {
	RecordedEventMetadata
	ExpectedRevision Revision

	encoder    Encoder
	encoderSet bool
}

// WithEncoder overrides the store's constructor encoder for one publish
// (ENCODING-S2.R3, ADR-0007). The override is explicit at the call site; a
// nil override is a publish error, never a silent fallback (ENCODING-S2.R5).
func WithEncoder(encoder Encoder) PublishOption {
	return func(modifier *PublishOptions) {
		modifier.encoder = encoder
		modifier.encoderSet = true
	}
}

// EncoderFor resolves the encoder a publish must use: the explicit per-publish
// override when present, otherwise the store's own encoder. It returns an
// error only when the override is an explicit nil — NilEncoder, per
// ENCODING-S2.R5; stores must fail the publish with that error prefixed with
// the store name and record nothing.
func (o PublishOptions) EncoderFor(storeEncoder Encoder) (Encoder, error) {
	if !o.encoderSet {
		return storeEncoder, nil
	}
	if o.encoder == nil {
		return nil, NilEncoder
	}
	return o.encoder, nil
}

type PublishOption func(modifier *PublishOptions)

func Options(options ...PublishOption) PublishOptions {
	modifiers := &PublishOptions{}
	for _, option := range options {
		option(modifiers)
	}

	return *modifiers
}

func WithExpectedRevision(expectedRevision Revision) PublishOption {
	return func(modifier *PublishOptions) {
		modifier.ExpectedRevision = expectedRevision
	}
}

func WithCorrelationId(correlationId CorrelationID) PublishOption {
	return func(modifier *PublishOptions) {
		modifier.CorrelationId = correlationId
	}
}

func WithCausationId(correlationId CorrelationID, causationId EventID) PublishOption {
	return func(modifier *PublishOptions) {
		modifier.CausationId = causationId
		modifier.CorrelationId = correlationId
	}
}
