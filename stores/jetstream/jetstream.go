package jetstream

import (
  "context"
  "encoding/binary"
  "encoding/json"
  "github.com/nats-io/nats.go"
  "github.com/oklog/ulid/v2"
  "github.com/rs/zerolog/log"
  "time"

  "github.com/weegigs/wee-events-go/we"
)

type EventStoreOption func(*EventStore)

func WithIdGenerator(generator IDGenerator) EventStoreOption {
  return func(store *EventStore) {
    store.id = generator
  }
}

func WithClockGenerator(clock Clock) EventStoreOption {
  return func(store *EventStore) {
    store.clock = clock
  }
}

type defaultClock struct {
}

func (defaultClock) Now() time.Time {
  return time.Now()
}

func NewDefaultIdGenerator(clock Clock) IDGenerator {
  return &DefaultIdGenerator{
    clock: clock,
  }
}

type DefaultIdGenerator struct {
  clock Clock
}

func (g *DefaultIdGenerator) Create() we.EventID {
  v := ulid.MustNew(ulid.Timestamp(g.clock.Now()), ulid.DefaultEntropy()).String()
  return we.EventID(v)
}

type Marshaller interface {
  Unmarshal(data []byte, v any) error
  Marshal(v any) ([]byte, error)
}

type JSONMarshaller struct{}

func (J JSONMarshaller) Unmarshal(data []byte, v any) error {
  return json.Unmarshal(data, v)
}

func (J JSONMarshaller) Marshal(v any) ([]byte, error) {
  return json.Marshal(v)
}

func WithMarshaller(marshaller Marshaller) EventStoreOption {
  return func(store *EventStore) {
    store.marshaller = marshaller
  }
}

const prefix = "change-set."

func NewEventStore(name string, connection *nats.Conn, options ...EventStoreOption) *EventStore {
  stream, err := connection.JetStream()
  if err != nil {
    return nil
  }

  _, err = stream.AddStream(&nats.StreamConfig{
    Name:        name,
    Description: "change set stream for " + name,
    Subjects:    []string{prefix + ">"},
  })
  if err != nil {
    return nil
  }

  store := &EventStore{
    name:    name,
    manager: stream,
    stream:  stream,
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

  return store
}

type Clock interface {
  Now() time.Time
}

type IDGenerator interface {
  Create() we.EventID
}

type EventStore struct {
  name       string
  manager    nats.JetStreamManager
  stream     nats.JetStream
  clock      Clock
  id         IDGenerator
  marshaller Marshaller
}

func subject(aggregateId we.AggregateId) string {
  return prefix + aggregateId.Encode().String()
}

func (es *EventStore) Publish(ctx context.Context, aggregateId we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
  records := make([]EventRecord, len(events))

  for index, event := range events {
    data, err := encodeEvent(event)
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

  var opts = []nats.PubOpt{nats.Context(ctx)}

  expected := options.ExpectedRevision
  if expected != "" {
    if expected == we.InitialRevision {
      opts = append(opts, nats.ExpectLastSequencePerSubject(0))
    } else {
      sequenceNumber, err := es.decodeSequenceNumber(expected)
      if err != nil {
        return err
      }

      opts = append(opts, nats.ExpectLastSequencePerSubject(sequenceNumber))
    }
  }

  _, err = es.stream.Publish(subject(aggregateId), bytes, opts...)
  if err != nil {
    if api, ok := err.(*nats.APIError); ok {
      if api.ErrorCode == nats.JSErrCodeStreamWrongLastSequence {
        return we.RevisionConflict
      }
    }
    return err
  }

  return nil
}

func encodeEvent(event we.DomainEvent) (we.Data, error) {
  return we.MarshalToData(event)
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
  msg, err := es.manager.GetLastMsg(es.name, subject, nats.Context(ctx))
  if err != nil {
    if err == nats.ErrMsgNotFound {
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

  subscription, err := es.stream.SubscribeSync(subject, nats.DeliverAll(), nats.OrderedConsumer())
  if err != nil {
    return nil, err
  }
  defer func(subscription *nats.Subscription) {
    err := subscription.Unsubscribe()
    if err != nil {
      log.Err(err).Msg("ephemeral stream subscription failed to unsubscribe cleanly")
    }
  }(subscription)

  var events []we.RecordedEvent
  for {
    msg, err := subscription.NextMsgWithContext(ctx)
    if err != nil {
      return nil, err
    }

    metadata, err := msg.Metadata()
    if err != nil {
      return nil, err
    }

    recorded, err := es.decodeChangeSet(msg.Data, metadata)
    if err != nil {
      return nil, err
    }

    events = append(events, recorded...)

    if metadata.Sequence.Stream >= *latest {
      break
    }
  }

  return events, nil
}

func (es *EventStore) decodeChangeSet(data []byte, metadata *nats.MsgMetadata) ([]we.RecordedEvent, error) {
  cs := &ChangeSet{}
  err := es.marshaller.Unmarshal(data, cs)
  if err != nil {
    return nil, err
  }

  var result []we.RecordedEvent
  ts := ulid.Timestamp(metadata.Timestamp)
  timestamp := we.TimestampFromTime(metadata.Timestamp)

  for i, event := range cs.Events {
    revision, err := es.encodeRevision(ts, metadata.Sequence.Stream, uint16(i))
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

func (es *EventStore) encodeRevision(timestamp uint64, sequence uint64, index uint16) (we.Revision, error) {
  r := &ulid.ULID{}
  err := r.SetTime(timestamp)

  if err != nil {
    return "", err
  }

  entropy := make([]byte, 10)
  binary.BigEndian.PutUint64(entropy[:8], sequence)
  binary.BigEndian.PutUint16(entropy[8:], index)

  err = r.SetEntropy(entropy)
  if err != nil {
    return "", err
  }

  return we.Revision(r.String()), nil
}

func (es *EventStore) decodeSequenceNumber(revision we.Revision) (uint64, error) {
  parsed, err := ulid.Parse(revision.String())
  if err != nil {
    return 0, err
  }

  sequence := binary.BigEndian.Uint64(parsed.Entropy()[:8])

  return sequence, nil
}
