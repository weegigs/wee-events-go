package we

import (
	"fmt"
)

func UnexpectedEvent(event *RecordedEvent) error {
	return fmt.Errorf("unexpected event %s", event.EventType)
}

func UnexpectedCommand(command Command) error {
	return fmt.Errorf("unexpected command %s", CommandNameOf(command))
}
