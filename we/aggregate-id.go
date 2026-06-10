package we

import (
	"fmt"
	"strings"
)

// Reasons an aggregate identity is rejected (IDENTITY-S1.R6). The set is
// closed so callers classify on the constant, never on message text.
const (
	ReasonEmptyType        = "empty-type"
	ReasonEmptyKey         = "empty-key"
	ReasonInvalidType      = "invalid-type"
	ReasonInvalidKey       = "invalid-key"
	ReasonMissingSeparator = "missing-separator"
)

// InvalidAggregateIdError reports a rejected aggregate identity, carrying the
// offending parts and the closed-set Reason.
type InvalidAggregateIdError struct {
	Type   string
	Key    string
	Reason string
}

func (e *InvalidAggregateIdError) Error() string {
	return fmt.Sprintf("invalid aggregate id (type %q, key %q): %s", e.Type, e.Key, e.Reason)
}

// identityTypeRunes / identityKeyRunes are the identity charsets
// (IDENTITY-S1.R4, ADR-0008): RFC 3986 unreserved for types, plus '|' — the
// composite-key segment separator — for keys (IDENTITY-S1.R8). They are
// defined by identity-domain concerns alone — legibility, lossless
// encodability, non-ambiguity (no ':', no '%', no whitespace, no pattern
// metacharacters) — never by any store's transport: stores adapt to this key
// space, encoding store-locally if their transport ever requires it
// (IDENTITY-S4).
const (
	identityTypeRunes = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	identityKeyRunes  = identityTypeRunes + "|"
)

// validIdentityPart reports whether s is non-empty, not a URL dot-segment, and
// made only of the given charset.
func validIdentityPart(s string, runes string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(runes, r) {
			return false
		}
	}
	return true
}

// MakeAggregateId is the validating constructor for untrusted identity parts
// (IDENTITY-S1). Emptiness is reported with its own reason; everything else
// outside the charsets is invalid-type/invalid-key. Keys are opaque: '|' is
// the documented composite convention, never parsed here.
func MakeAggregateId(aggregateType string, key string) (AggregateId, error) {
	switch {
	case aggregateType == "":
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonEmptyType}
	case key == "":
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonEmptyKey}
	case !validIdentityPart(aggregateType, identityTypeRunes):
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonInvalidType}
	case !validIdentityPart(key, identityKeyRunes):
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonInvalidKey}
	}
	return AggregateId{Type: aggregateType, Key: key}, nil
}
