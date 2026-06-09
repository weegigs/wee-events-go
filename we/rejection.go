package we

import (
	"encoding/json"
)

// Rejection is a domain refusal: a well-formed command that the aggregate's
// current state does not permit. It is a domain outcome, not an infrastructure
// failure (principle 3 — "state is not an error"): a handler returns it as its
// ordinary error and a boundary recovers it via errors.As to map it to a
// client-facing result rather than a server fault (ADR-0005).
//
// A Rejection is distinct from RevisionConflict, which is an
// optimistic-concurrency retry signal in the store/infrastructure lane and is
// never a Rejection.
type Rejection struct {
	// Code is a stable, machine-readable identifier for the refusal.
	Code string `json:"code"`
	// Message is a human-readable description of the refusal.
	Message string `json:"message"`
	// Context is optional structured detail. Raw JSON so callers receive
	// machine-readable detail without the framework imposing a schema.
	Context json.RawMessage `json:"context,omitempty"`
}

// MakeRejection builds a Rejection. It returns a value (Make* convention) so a
// handler can return it directly as its error; context may be nil when there is
// no structured detail to carry (REJECT-S1.R1).
func MakeRejection(code string, message string, context json.RawMessage) Rejection {
	return Rejection{
		Code:    code,
		Message: message,
		Context: context,
	}
}

// Error renders the rejection as "code: message" so it satisfies error and
// reads usefully in logs and wrapped error chains.
func (r Rejection) Error() string {
	return r.Code + ": " + r.Message
}
