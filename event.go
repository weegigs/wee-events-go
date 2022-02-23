package we

import (
	"encoding/json"
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
	Encoding string          `json:"encoding"`
	Data     json.RawMessage `json:"data"`
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
	AggregateId AggregateId           `json:"aggregate"`
	Revision    Revision              `json:"revision"`
	EventID     EventID               `json:"id"`
	EventType   EventType             `json:"type"`
	Timestamp   Timestamp             `json:"timestamp"`
	Metadata    RecordedEventMetadata `json:"metadata"`
	Data        Data                  `json:"data"`
}
