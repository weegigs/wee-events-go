package we

import (
	"errors"
	"strings"
)

type EventID string

func (id EventID) String() string {
	return string(id)
}

type EventType string

func (et EventType) String() string {
	return string(et)
}

type CorrelationID string

func (id CorrelationID) String() string {
	return string(id)
}

type Payload any
type Data struct {
	Encoding string `json:"encoding"`
	Data     []byte `json:"data"`
}

type AggregateId struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

type EncodedAggregateId string

func (id AggregateId) Encode() EncodedAggregateId {
	return EncodedAggregateId(strings.Join([]string{id.Type, id.Key}, "."))
}

func (id EncodedAggregateId) String() string {
	return string(id)
}

func (id EncodedAggregateId) Decode() (*AggregateId, error) {
	seperated := strings.Split(string(id), ".")
	if len(seperated) < 2 {
		return nil, errors.New("expected . delimiter in aggregate id")
	}

	return &AggregateId{
		Type: seperated[0],
		Key:  strings.Join(seperated[1:], "."),
	}, nil

}

type DomainEvent any

func EventTypeOf(event DomainEvent) EventType {
	return EventType(NameOf(event))
}

type RecordedEventMetadata struct {
	CausationId   EventID       `json:"causationId,omitempty"`
	CorrelationId CorrelationID `json:"correlationId,omitempty"`
}

type RecordedEvent struct {
	AggregateId AggregateId                   `json:"aggregate"`
	Revision    Revision                      `json:"revision"`
	EventID     EventID                       `json:"id"`
	EventType   EventType                     `json:"type"`
	Timestamp   Timestamp                     `json:"timestamp"`
	Metadata    RecordedEventMetadata         `json:"metadata"`
	Data        Data                          `json:"data"`
	Decode      func(event DomainEvent) error `json:"-"`
}

// type Initializer[T any] func(evt *RecordedEvent) (*T, error)

type Initializer[T any] interface {
	Initialize(evt *RecordedEvent) (*T, error)
}

type InitializerFunction[T any, E any] func(evt *E) (*T, error)

func (f InitializerFunction[T, E]) Initialize(evt *RecordedEvent) (*T, error) {
	var event E
	if err := evt.Decode(&event); err != nil {
		return nil, err
	}

	return f(&event)
}

type Reducer[T any] interface {
	Reduce(state *T, evt *RecordedEvent) error
}

type ReducerFunction[T any, E any] func(state *T, evt *E) error

func (f ReducerFunction[T, E]) Reduce(state *T, evt *RecordedEvent) error {
	var event E
	if err := evt.Decode(&event); err != nil {
		return err
	}

	return f(state, &event)
}
