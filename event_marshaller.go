package es

import (
  "encoding/json"
  "errors"
  "fmt"
  "strings"

  "github.com/iancoleman/strcase"
)

type EventMarshaller interface {
  Marshall(value any) (*DomainEvent, error)
  Unmarshall(event *RecordedEvent) (any, error)
}

type EventTyped interface {
  EventType() EventType
}

type Named interface {
  Name() string
}

func JsonEventMarshaller(marshal func(event *RecordedEvent) (any, error)) EventMarshaller {
  return jsonMarshaller{marshal: marshal}
}

type jsonMarshaller struct {
  marshal func(event *RecordedEvent) (any, error)
}

func (m jsonMarshaller) nameForValue(value any) EventType {
  var name EventType
  if typed, ok := value.(EventTyped); ok == true {
    name = typed.EventType()
  } else {

    split := strings.Split(fmt.Sprintf("%T", value), ".")
    segments := make([]string, len(split))
    for i, segment := range split {
      segments[i] = strcase.ToKebab(segment)
    }

    namespace := segments[0]
    event := strings.Join(segments[1:], "-")

    return EventType(namespace + ":" + event)
  }

  return name
}

func (m jsonMarshaller) Marshall(value any) (*DomainEvent, error) {
  data, err := json.Marshal(value)
  if err != nil {
    return nil, err
  }

  t := m.nameForValue(value)
  return &DomainEvent{
    EventType: t,
    Data:      data,
  }, nil
}

func (m jsonMarshaller) Unmarshall(event *RecordedEvent) (any, error) {
  return m.marshal(event)
}

func DefaultJsonEventMarshaller() EventMarshaller {
  return jsonMarshaller{
    func(event *RecordedEvent) (any, error) {
      var v map[string]any
      if err := json.Unmarshal(event.Data, &v); err != nil {
        return nil, err
      }

      return v, nil
    },
  }
}

func CompositeMarshaller(marshallers ...EventMarshaller) EventMarshaller {
  return compositeMarshaller{marshallers: marshallers}
}

type compositeMarshaller struct {
  marshallers []EventMarshaller
}

func (c compositeMarshaller) Marshall(value any) (*DomainEvent, error) {
  for _, marshaller := range c.marshallers {
    event, err := marshaller.Marshall(value)
    if err != nil {
      return nil, err
    }

    if event != nil {
      return event, nil
    }
  }

  return nil, errors.New(fmt.Sprintf("no marshaller found for %T", value))
}

func (c compositeMarshaller) Unmarshall(event *RecordedEvent) (any, error) {
  return nil, nil
}
