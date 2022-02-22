package we

import (
	"errors"
	"fmt"
)

func UnexpectedEvent(event *RecordedEvent) error {
	return errors.New(fmt.Sprintf("unexpected event %s", event.EventType))
}

func UnexpectedCommand(command Command) error {
	return errors.New(fmt.Sprintf("unexpected command %s", CommandNameOf(command)))
}
