package we

import (
	"maps"
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
	// Fields is optional structured detail carried as the closed scalar field
	// model shared with wee-events.rs (option A decision, 2026-07-09): flat
	// scalar fields, never opaque JSON, so the detail stays branchable and
	// lossless on both sides of every boundary.
	Fields map[string]ErrorField `json:"fields,omitempty"`
}

// MakeRejection builds a Rejection. It returns a value (Make* convention) so a
// handler can return it directly as its error; fields may be nil when there is
// no structured detail to carry (REJECT-S1.R1).
func MakeRejection(code string, message string, fields map[string]ErrorField) Rejection {
	return Rejection{
		Code:    code,
		Message: message,
		Fields:  fields,
	}
}

// Error renders the rejection as "code: message" so it satisfies error and
// reads usefully in logs and wrapped error chains.
func (r Rejection) Error() string {
	return r.Code + ": " + r.Message
}

// ToErrorFrame renders the rejection as its cross-boundary frame. The fields
// map is cloned so the frame and the rejection never alias.
func (r Rejection) ToErrorFrame() ErrorFrame {
	return ErrorFrame{
		Code:    r.Code,
		Message: r.Message,
		Fields:  maps.Clone(r.Fields),
	}
}
