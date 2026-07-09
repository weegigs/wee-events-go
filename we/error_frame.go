package we

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrorField is one value in an error frame's field map. The variant set is
// closed and shared verbatim with wee-events.rs (Text, I64, U64, Bool) so a
// declared service error stays branchable and lossless across implementations
// — flat scalars, never opaque blobs (option A decision, 2026-07-09; see
// wee-events.rs documents/plans/2026-06-22-restate-service-error-contract-design.md).
// The zero value is invalid and fails to encode.
type ErrorField struct {
	kind fieldKind
	text string
	i64  int64
	u64  uint64
	b    bool
}

type fieldKind uint8

// The zero fieldKind is deliberately unnamed and invalid: a zero-value
// ErrorField matches no variant and fails to encode.
const (
	fieldText fieldKind = iota + 1
	fieldI64
	fieldU64
	fieldBool
)

// MakeTextField builds a Text field.
func MakeTextField(value string) ErrorField {
	return ErrorField{kind: fieldText, text: value}
}

// MakeI64Field builds an I64 field.
func MakeI64Field(value int64) ErrorField {
	return ErrorField{kind: fieldI64, i64: value}
}

// MakeU64Field builds a U64 field.
func MakeU64Field(value uint64) ErrorField {
	return ErrorField{kind: fieldU64, u64: value}
}

// MakeBoolField builds a Bool field.
func MakeBoolField(value bool) ErrorField {
	return ErrorField{kind: fieldBool, b: value}
}

// Text returns the value when the field is a Text variant.
func (f ErrorField) Text() (string, bool) {
	return f.text, f.kind == fieldText
}

// I64 returns the value when the field is an I64 variant.
func (f ErrorField) I64() (int64, bool) {
	return f.i64, f.kind == fieldI64
}

// U64 returns the value when the field is a U64 variant.
func (f ErrorField) U64() (uint64, bool) {
	return f.u64, f.kind == fieldU64
}

// Bool returns the value when the field is a Bool variant.
func (f ErrorField) Bool() (bool, bool) {
	return f.b, f.kind == fieldBool
}

// MarshalJSON renders the serde externally-tagged encoding, e.g. {"I64":50}.
func (f ErrorField) MarshalJSON() ([]byte, error) {
	switch f.kind {
	case fieldText:
		return json.Marshal(map[string]string{"Text": f.text})
	case fieldI64:
		return json.Marshal(map[string]int64{"I64": f.i64})
	case fieldU64:
		return json.Marshal(map[string]uint64{"U64": f.u64})
	case fieldBool:
		return json.Marshal(map[string]bool{"Bool": f.b})
	default:
		return nil, errors.New("we: cannot encode a zero-value error field")
	}
}

// UnmarshalJSON decodes the externally-tagged encoding. The variant set is
// closed: an unknown tag is a contract violation and fails the decode.
func (f *ErrorField) UnmarshalJSON(data []byte) error {
	var tagged map[string]json.RawMessage
	if err := json.Unmarshal(data, &tagged); err != nil {
		return fmt.Errorf("we: error field is not a tagged object: %w", err)
	}
	if len(tagged) != 1 {
		return fmt.Errorf("we: error field must carry exactly one variant tag, got %d", len(tagged))
	}
	for tag, raw := range tagged {
		switch tag {
		case "Text":
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("we: error field Text payload: %w", err)
			}
			*f = MakeTextField(value)
		case "I64":
			var value int64
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("we: error field I64 payload: %w", err)
			}
			*f = MakeI64Field(value)
		case "U64":
			var value uint64
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("we: error field U64 payload: %w", err)
			}
			*f = MakeU64Field(value)
		case "Bool":
			var value bool
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("we: error field Bool payload: %w", err)
			}
			*f = MakeBoolField(value)
		default:
			return fmt.Errorf("we: unknown error field tag %q", tag)
		}
	}
	return nil
}
