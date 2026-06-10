package we

import (
	"fmt"
)

// Reasons an aggregate identity is rejected. The set is closed so callers
// classify on the constant, never on message text
// (documents/spec/aggregate-identity.md §Rejection reasons).
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

// Length caps in octets (documents/spec/aggregate-identity.md §Grammar).
const (
	maxIdentityTypeOctets = 64
	maxIdentityKeyOctets  = 512
)

// identityTypeRunes / identityKeyRunes list every rune the grammar admits in
// each part (documents/spec/aggregate-identity.md). Placement rules — token
// and segment shape, length caps, the whole-key dot rule — live in the
// validators below; the spec is normative, these constants are not.
const (
	identityTypeRunes = "abcdefghijklmnopqrstuvwxyz0123456789-"
	identityKeyRunes  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._@|"
)

// validIdentityType implements the type grammar: kebab-case tokens of
// [a-z0-9] joined by single hyphens, first token starting with a letter,
// at most 64 octets. Byte iteration is exact — any non-ASCII byte falls to
// the default arm.
func validIdentityType(s string) bool {
	if s == "" || len(s) > maxIdentityTypeOctets {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	previousHyphen := false
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case ('a' <= c && c <= 'z') || ('0' <= c && c <= '9'):
			previousHyphen = false
		case c == '-':
			if previousHyphen {
				return false
			}
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

// validIdentityKey implements the key grammar: segments of
// [A-Za-z0-9._@-] joined by single pipes, at most 512 octets, and the key
// as a whole is never "." or ".." (the URL dot-segment rule is whole-key
// only — interior dots are opaque data).
func validIdentityKey(s string) bool {
	if s == "" || len(s) > maxIdentityKeyOctets || s == "." || s == ".." {
		return false
	}
	previousBoundary := true // the start of the key opens a segment
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9'),
			c == '-', c == '.', c == '_', c == '@':
			previousBoundary = false
		case c == '|':
			if previousBoundary {
				return false
			}
			previousBoundary = true
		default:
			return false
		}
	}
	return !previousBoundary
}

// MakeAggregateId is the validating constructor for untrusted identity parts
// (IDENTITY-S1; normative grammar documents/spec/aggregate-identity.md).
// Emptiness is reported with its own reason; every other violation —
// charset, shape, or length — is invalid-type/invalid-key. Keys are
// semantically opaque: the grammar guarantees segment well-formedness,
// nothing here interprets segments.
func MakeAggregateId(aggregateType string, key string) (AggregateId, error) {
	switch {
	case aggregateType == "":
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonEmptyType}
	case key == "":
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonEmptyKey}
	case !validIdentityType(aggregateType):
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonInvalidType}
	case !validIdentityKey(key):
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonInvalidKey}
	}
	return AggregateId{Type: aggregateType, Key: key}, nil
}
