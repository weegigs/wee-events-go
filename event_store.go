package es

import (
  "context"
  "errors"
)

type EventLoader = func(ctx context.Context, id AggregateId) (*Aggregate, error)
type EventPublisher = func(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...any) (Revision, error)

type EventStore interface {
  Load(ctx context.Context, id AggregateId) (*Aggregate, error)
  Publish(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...any) (Revision, error)
}

func Loader(store EventStore) EventLoader {
  return store.Load
}

func Publisher(store EventStore) EventPublisher {
  return store.Publish
}

var RevisionConflict = errors.New("revision-conflict")

type PublishOptions struct {
  RecordedEventMetadata
  ExpectedRevision Revision
  Encrypt          bool
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
    modifier.RecordedEventMetadata.CorrelationId = correlationId
  }
}

func WithCausationId(correlationId CorrelationID, causationId EventID) PublishOption {
  return func(modifier *PublishOptions) {
    modifier.RecordedEventMetadata.CausationId = causationId
    modifier.RecordedEventMetadata.CorrelationId = correlationId
  }
}

func WithEncryption() PublishOption {
  return func(modifier *PublishOptions) {
    modifier.Encrypt = true
  }
}
