package es

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
type Data []byte

type AggregateId struct {
  Type string `dynamodbav:"type" json:"type"`
  Key  string `dynamodbav:"key" json:"key"`
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

type DomainEvent struct {
  EventType EventType `dynamodbav:"type" json:"type"`
  Data      Data      `dynamodbav:"data" json:"data"`
}

type RecordedEventMetadata struct {
  CausationId   EventID       `dynamodbav:"causationId,omitempty" json:"causationId,omitempty"`
  CorrelationId CorrelationID `dynamodbav:"correlationId,omitempty" json:"correlationId,omitempty"`
}

type RecordedEvent struct {
  AggregateId AggregateId           `dynamodbav:"aggregate" json:"aggregate"`
  Revision    Revision              `dynamodbav:"revision" json:"revision"`
  EventID     EventID               `dynamodbav:"id" json:"id"`
  EventType   EventType             `dynamodbav:"type" json:"type"`
  Timestamp   Timestamp             `dynamodbav:"timestamp" json:"timestamp"`
  Metadata    RecordedEventMetadata `dynamodbav:"metadata" json:"metadata"`
  Data        Data                  `dynamodbav:"data" json:"data"`
}

type DecodedEvent struct {
  RecordedEvent
  Payload Payload
}
