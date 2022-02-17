package es

import (
  "errors"
  "fmt"
)

func UnexpectedEvent(event *DecodedEvent) error {
  return errors.New(fmt.Sprintf("unexpected event %s", event.EventType))
}

func UnexpectedCommand(command Command) error {
  return errors.New(fmt.Sprintf("unexpected command %s", command.Type()))
}
