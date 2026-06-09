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

// DecodeError reports that an inbound command payload could not be decoded:
// either it declared an unsupported/unknown encoding or it carried malformed
// bytes for a known encoding. It is a CLIENT error — the request was bad —
// distinct from a store or stored-event infrastructure failure (which is a
// server fault) and from a domain Rejection (a well-formed-but-refused command).
//
// Boundaries recover it via errors.As to map an inbound-decode failure to a 4xx
// (HTTP) or a terminal error (Restate), while a store/transport failure remains
// a 5xx / retryable. The underlying decode error is wrapped (not discarded) so
// the original typed cause — e.g. *InvalidEncodingError — stays recoverable
// (ADR-0005, REJECT-S3.R1).
type DecodeError struct {
	// Command is the declared name of the command whose payload failed to decode.
	Command CommandName
	// Err is the underlying decode failure.
	Err error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("failed to decode command %s: %s", e.Command, e.Err)
}

func (e *DecodeError) Unwrap() error {
	return e.Err
}

// CommandDecodeFailed wraps an inbound-payload decode failure as a typed
// *DecodeError so a boundary can positively classify it as a client error.
func CommandDecodeFailed(command CommandName, err error) error {
	return &DecodeError{Command: command, Err: err}
}
