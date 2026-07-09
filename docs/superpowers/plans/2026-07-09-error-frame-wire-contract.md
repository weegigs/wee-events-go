# Error-Frame Wire Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Bead:** wee-events-go-tu2
**Goal:** Implement the `wee-events:error-frame+json:` wire contract so declared service errors round-trip between Go and Rust across the Restate boundary as branchable, typed values — never opaque display strings.

**Architecture:** Three layers, matching the Rust design (wee-events.rs `documents/plans/2026-06-22-restate-service-error-contract-design.md`): (1) transport-neutral frame types in `we/` (`ErrorFrame`, closed scalar `ErrorField` set, `ServiceErrorContract`); (2) an adapter-private frame codec in `connectors/werestate/` that smuggles the frame through Restate's `TerminalError.message` string behind the `wee-events:error-frame+json:` prefix; (3) a typed boundary client (`werestate.Client`) that decodes frames back into declared errors and keeps transport failures in a distinct `*TransportError` lane. Per the option A decision (recorded on the bead, 2026-07-09), `we.Rejection` migrates from opaque `Context json.RawMessage` to flat scalar `Fields map[string]ErrorField`.

**Tech Stack:** Go 1.26 (via mise), `encoding/json` (hand-written externally-tagged codec matching serde), `net/http` + `httptest` for the client, testcontainers for the integration round-trip, testify for assertions.

## Global Constraints

- Toolchain via mise: run Go as `mise exec -- go <args>`; project tasks via `just <recipe>`.
- Idiomatic Go only (owner directive, bd memory `ports-from-wee-events-rs-must-stay-idiomatic`): no Result wrappers, no enum/macro emulation. Sum-type behaviour is expressed as an opaque struct with constructors and comma-ok accessors; Declared-vs-Transport is expressed as distinct error types recovered via `errors.As`.
- Wire compatibility is byte-exact with Rust: prefix `wee-events:error-frame+json:`, serde externally-tagged fields (`{"I64":50}`), struct key order `code`,`message`,`fields`, map keys sorted (Go `encoding/json` sorts map keys; Rust uses `BTreeMap` — they agree).
- Unknown field tags MUST fail decode, never pass silently (option A decision; user CLAUDE.md "string-to-enum unknown values must error").
- No lint suppressions. `just lint` must pass clean.
- Constructors: `New*` returns pointer, `Make*` returns value (documents/conventions.md).
- Commit workflow: jj split (working copy `@` keeps no description). Commit messages in past tense, objective voice, no AI co-writer notes.
- Comments may cite spec requirement IDs (REJECT-S1.R1 style), matching existing files.

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `we/error_frame.go` | Create | `ErrorField`, `ErrorFrame`, `ServiceErrorContract` — transport-neutral contract types |
| `we/error_frame_test.go` | Create | Codec tests incl. vendored Rust wire vector |
| `we/rejection.go` | Modify | `Context json.RawMessage` → `Fields map[string]ErrorField`; `ToErrorFrame` |
| `we/rejection_test.go` | Modify | Field-model assertions |
| `we/rejection_service_test.go` | Modify | Field-model assertions |
| `samples/account/handlers.go` | Modify | Withdraw guard uses typed fields (drops `json.Marshal` dance) |
| `connectors/wehttp/http.go` | Modify | `rejectionBody`/`marshalRejection` render flattened fields as `context` |
| `connectors/wehttp/http_test.go` | Modify | Updated body assertions |
| `connectors/werestate/frame_codec.go` | Create | Prefix codec + `framedError` |
| `connectors/werestate/frame_codec_test.go` | Create | Mirrors Rust `frame_codec.rs` tests |
| `connectors/werestate/restate.go` | Modify | `mapError` emits framed rejections |
| `connectors/werestate/restate_test.go` | Modify | Wire-shape assertions added |
| `connectors/werestate/client.go` | Create | Typed boundary client, `TransportError`, `FrameDecoder` |
| `connectors/werestate/client_test.go` | Create | httptest-based lane tests |
| `connectors/werestate/integration_test.go` | Modify | Cross-boundary rejection round-trip through real Restate |
| `documents/features/05-rejection-error-taxonomy.md` | Modify | Rejection shape: fields, not raw JSON context |
| `documents/features/09-error-surfacing.md` | Modify | Account context wording |

---

### Task 1: `ErrorField` — closed scalar set with serde-compatible JSON

**Files:**
- Create: `we/error_frame.go`
- Test: `we/error_frame_test.go`

**Interfaces:**
- Consumes: nothing (leaf task).
- Produces: `type ErrorField struct` (opaque); constructors `MakeTextField(string) ErrorField`, `MakeI64Field(int64) ErrorField`, `MakeU64Field(uint64) ErrorField`, `MakeBoolField(bool) ErrorField`; accessors `(ErrorField) Text() (string, bool)`, `I64() (int64, bool)`, `U64() (uint64, bool)`, `Bool() (bool, bool)`; `MarshalJSON`/`UnmarshalJSON` (externally tagged). Zero-value `ErrorField` fails `MarshalJSON`.

- [ ] **Step 1: Write the failing test**

Create `we/error_frame_test.go`:

```go
package we

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The wire encoding is serde's externally-tagged form, byte-compatible with
// wee-events.rs `ErrorField` (crates/wee-events/src/service_error_contract.rs).
func errorFieldEncodesExternallyTagged(t *testing.T) {
	cases := []struct {
		name  string
		field ErrorField
		wire  string
	}{
		{"text", MakeTextField("boots"), `{"Text":"boots"}`},
		{"i64", MakeI64Field(50), `{"I64":50}`},
		{"u64", MakeU64Field(18446744073709551615), `{"U64":18446744073709551615}`},
		{"bool", MakeBoolField(true), `{"Bool":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.field)
			require.NoError(t, err)
			assert.Equal(t, tc.wire, string(data))

			var decoded ErrorField
			require.NoError(t, json.Unmarshal(data, &decoded))
			assert.Equal(t, tc.field, decoded)
		})
	}
}

func errorFieldAccessorsAreCommaOk(t *testing.T) {
	text, ok := MakeTextField("boots").Text()
	require.True(t, ok)
	assert.Equal(t, "boots", text)

	_, ok = MakeTextField("boots").I64()
	assert.False(t, ok, "a Text field must not read as I64")

	i, ok := MakeI64Field(-7).I64()
	require.True(t, ok)
	assert.Equal(t, int64(-7), i)

	u, ok := MakeU64Field(7).U64()
	require.True(t, ok)
	assert.Equal(t, uint64(7), u)

	b, ok := MakeBoolField(true).Bool()
	require.True(t, ok)
	assert.True(t, b)
}

// Unknown tags fail decode — the scalar set is closed; silently passing an
// unrecognised variant would mask a contract mismatch between implementations.
func errorFieldRejectsUnknownTag(t *testing.T) {
	var field ErrorField
	err := json.Unmarshal([]byte(`{"F64":1.5}`), &field)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "F64")
}

func errorFieldRejectsMalformedShapes(t *testing.T) {
	var field ErrorField
	assert.Error(t, json.Unmarshal([]byte(`{}`), &field), "no tag")
	assert.Error(t, json.Unmarshal([]byte(`{"I64":1,"Bool":true}`), &field), "two tags")
	assert.Error(t, json.Unmarshal([]byte(`{"I64":"fifty"}`), &field), "wrong payload type")
	assert.Error(t, json.Unmarshal([]byte(`{"I64":1.5}`), &field), "non-integer payload")
	assert.Error(t, json.Unmarshal([]byte(`"bare"`), &field), "not an object")
}

// A zero-value ErrorField is a programmer error, not a variant; encoding it
// must fail loudly rather than invent wire content.
func zeroErrorFieldFailsToEncode(t *testing.T) {
	_, err := json.Marshal(ErrorField{})
	assert.Error(t, err)
}

func TestErrorField(t *testing.T) {
	t.Run("encodes externally tagged", errorFieldEncodesExternallyTagged)
	t.Run("accessors are comma-ok", errorFieldAccessorsAreCommaOk)
	t.Run("rejects unknown tag", errorFieldRejectsUnknownTag)
	t.Run("rejects malformed shapes", errorFieldRejectsMalformedShapes)
	t.Run("zero value fails to encode", zeroErrorFieldFailsToEncode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./we/ -run TestErrorField -v`
Expected: FAIL to compile — `undefined: ErrorField`, `undefined: MakeTextField`, etc.

- [ ] **Step 3: Write minimal implementation**

Create `we/error_frame.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./we/ -run TestErrorField -v`
Expected: PASS (all five subtests)

- [ ] **Step 5: Commit**

```bash
jj split -m "Added the ErrorField closed scalar set with serde-compatible externally-tagged JSON" we/error_frame.go we/error_frame_test.go
```

---

### Task 2: `ErrorFrame`, `ServiceErrorContract`, and the vendored Rust wire vector

**Files:**
- Modify: `we/error_frame.go` (append)
- Test: `we/error_frame_test.go` (append)

**Interfaces:**
- Consumes: `ErrorField` and its constructors from Task 1.
- Produces: `type ErrorFrame struct { Code string; Message string; Fields map[string]ErrorField }` with `MarshalJSON`/`UnmarshalJSON`; `type ServiceErrorContract interface { error; ToErrorFrame() ErrorFrame }`.

- [ ] **Step 1: Write the failing test**

Append to `we/error_frame_test.go`:

```go
// rustFrameVector is the exact wire produced by wee-events.rs for the frame in
// crates/wee-events-restate/src/frame_codec.rs tests (serde_json over a struct
// with a BTreeMap: struct keys in declaration order, map keys sorted). Vendored
// until the shared conformance repository exists (wee-events-go-2sl).
const rustFrameVector = `{"code":"time.clock_drift_too_high","message":"time server clock drift too high","fields":{"allowed_ms":{"I64":10},"observed_ms":{"I64":50}}}`

func rustClockDriftFrame() ErrorFrame {
	return ErrorFrame{
		Code:    "time.clock_drift_too_high",
		Message: "time server clock drift too high",
		Fields: map[string]ErrorField{
			"observed_ms": MakeI64Field(50),
			"allowed_ms":  MakeI64Field(10),
		},
	}
}

// Byte-exact both directions: Go must decode what Rust encodes and encode what
// Rust decodes. Go's encoding/json sorts map keys; serde_json serialises the
// BTreeMap sorted — the renderings agree byte for byte.
func errorFrameMatchesRustWireVector(t *testing.T) {
	data, err := json.Marshal(rustClockDriftFrame())
	require.NoError(t, err)
	assert.Equal(t, rustFrameVector, string(data))

	var decoded ErrorFrame
	require.NoError(t, json.Unmarshal([]byte(rustFrameVector), &decoded))
	assert.Equal(t, rustClockDriftFrame(), decoded)
}

// A frame with no fields still carries "fields":{} — serde requires the key
// (BTreeMap without #[serde(default)]), so omitting it would break Rust decode.
func errorFrameAlwaysCarriesFieldsKey(t *testing.T) {
	data, err := json.Marshal(ErrorFrame{Code: "order.closed", Message: "order is closed"})
	require.NoError(t, err)
	assert.Equal(t, `{"code":"order.closed","message":"order is closed","fields":{}}`, string(data))
}

// Mirroring serde's strictness: a frame without the fields key (or with null)
// is not a valid frame.
func errorFrameRequiresFieldsOnDecode(t *testing.T) {
	var frame ErrorFrame
	assert.Error(t, json.Unmarshal([]byte(`{"code":"a","message":"b"}`), &frame), "missing fields")
	assert.Error(t, json.Unmarshal([]byte(`{"code":"a","message":"b","fields":null}`), &frame), "null fields")
}

func TestErrorFrame(t *testing.T) {
	t.Run("matches the Rust wire vector", errorFrameMatchesRustWireVector)
	t.Run("always carries the fields key", errorFrameAlwaysCarriesFieldsKey)
	t.Run("requires fields on decode", errorFrameRequiresFieldsOnDecode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./we/ -run TestErrorFrame -v`
Expected: FAIL to compile — `undefined: ErrorFrame`

- [ ] **Step 3: Write minimal implementation**

Append to `we/error_frame.go`:

```go
// ErrorFrame is the shared cross-boundary representation of a declared service
// error: a stable code, a human-readable message, and flat scalar fields. It is
// the presentation contract for errors (ADR-0011 layering) — JSON is one codec
// for it, owned by the transport edge, never by this type's consumers.
type ErrorFrame struct {
	Code    string
	Message string
	Fields  map[string]ErrorField
}

// errorFrameWire fixes the JSON key order (code, message, fields) to match the
// Rust struct declaration order, keeping the encodings byte-identical.
type errorFrameWire struct {
	Code    string                `json:"code"`
	Message string                `json:"message"`
	Fields  map[string]ErrorField `json:"fields"`
}

// MarshalJSON always emits the fields key ("fields":{} when empty): the Rust
// decoder requires it.
func (f ErrorFrame) MarshalJSON() ([]byte, error) {
	fields := f.Fields
	if fields == nil {
		fields = map[string]ErrorField{}
	}
	return json.Marshal(errorFrameWire{Code: f.Code, Message: f.Message, Fields: fields})
}

// UnmarshalJSON mirrors serde's strictness: a payload without a fields object
// is not a frame.
func (f *ErrorFrame) UnmarshalJSON(data []byte) error {
	var wire errorFrameWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Fields == nil {
		return errors.New("we: error frame missing fields object")
	}
	f.Code = wire.Code
	f.Message = wire.Message
	f.Fields = wire.Fields
	return nil
}

// ServiceErrorContract is the boundary-facing face of a declared service
// error: any error a caller is expected to branch on across a transport must
// render itself as an ErrorFrame. It is a service concept, not a transport
// one — implementations must not leak JSON, HTTP statuses, or Restate types.
type ServiceErrorContract interface {
	error
	ToErrorFrame() ErrorFrame
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./we/ -run 'TestErrorFrame|TestErrorField' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
jj split -m "Added ErrorFrame and ServiceErrorContract with byte-exact conformance to the Rust wire vector" we/error_frame.go we/error_frame_test.go
```

---

### Task 3: Migrate `Rejection` to the closed field model

**Files:**
- Modify: `we/rejection.go`
- Modify: `we/rejection_test.go`
- Modify: `we/rejection_service_test.go:62-75`
- Modify: `samples/account/handlers.go:34-40`
- Modify: `connectors/wehttp/http.go:106-142` (and `writeCommandError` comment block)
- Modify: `connectors/wehttp/http_test.go:56-97,558-585`
- Modify: `connectors/werestate/restate_test.go:252-285`

This task is compile-coupled: `MakeRejection`'s signature changes, so every caller updates in one atomic change. The build must pass at the end of this task and at no intermediate commit.

**Interfaces:**
- Consumes: `ErrorField`, `ErrorFrame`, `ServiceErrorContract`, `MakeI64Field` (Tasks 1–2).
- Produces: `Rejection{Code string; Message string; Fields map[string]ErrorField}`; `MakeRejection(code string, message string, fields map[string]ErrorField) Rejection`; `(Rejection) ToErrorFrame() ErrorFrame`. wehttp-internal: `flattenFields(map[string]we.ErrorField) (map[string]any, error)`.

- [ ] **Step 1: Write the failing tests**

Replace `we/rejection_test.go` functions `rejectionCarriesCodeMessageContext`, `rejectionWithoutContextIsValid`, and `rejectionRecoverableViaErrorsAs` (the other functions and `TestRejection` runner names for them stay, with runner labels updated):

```go
// REJECT-S1.R1 - the framework provides a Rejection value type carrying a code,
// a message, and structured fields from the closed scalar set, and it satisfies
// the error interface.
func rejectionCarriesCodeMessageFields(t *testing.T) {
	rejection := MakeRejection("customer.cancelled", "customer is already cancelled",
		map[string]ErrorField{"state": MakeTextField("cancelled")})

	assert.Equal(t, "customer.cancelled", rejection.Code)
	assert.Equal(t, "customer is already cancelled", rejection.Message)
	state, ok := rejection.Fields["state"].Text()
	require.True(t, ok)
	assert.Equal(t, "cancelled", state)

	var asError error = rejection
	assert.Equal(t, "customer.cancelled: customer is already cancelled", asError.Error())
}

// REJECT-S1.R1 - a Rejection constructed without fields is still a valid error.
func rejectionWithoutFieldsIsValid(t *testing.T) {
	rejection := MakeRejection("order.closed", "order is closed", nil)

	assert.Nil(t, rejection.Fields)
	assert.Equal(t, "order.closed: order is closed", rejection.Error())
}

// REJECT-S1.R2, REJECT-S4.R1 - a Rejection returned as a plain error is
// recoverable through the error chain via errors.As, even when wrapped.
func rejectionRecoverableViaErrorsAs(t *testing.T) {
	original := MakeRejection("order.closed", "order is closed",
		map[string]ErrorField{"reason": MakeTextField("closed")})

	var err error = original
	err = fmt.Errorf("dispatch failed: %w", err)

	var recovered Rejection
	require.True(t, errors.As(err, &recovered), "expected to recover a Rejection, got %T", err)
	assert.Equal(t, "order.closed", recovered.Code)
	assert.Equal(t, "order is closed", recovered.Message)
	reason, ok := recovered.Fields["reason"].Text()
	require.True(t, ok)
	assert.Equal(t, "closed", reason)
}

// A Rejection is a declared service error: its frame carries the same code,
// message, and fields, so it round-trips any conformant boundary losslessly.
func rejectionSatisfiesServiceErrorContract(t *testing.T) {
	var _ ServiceErrorContract = Rejection{}

	rejection := MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]ErrorField{"balance": MakeI64Field(0), "requested": MakeI64Field(100)})

	frame := rejection.ToErrorFrame()
	assert.Equal(t, ErrorFrame{
		Code:    "account.insufficient-funds",
		Message: "insufficient funds",
		Fields: map[string]ErrorField{
			"balance":   MakeI64Field(0),
			"requested": MakeI64Field(100),
		},
	}, frame)
}
```

Update the `TestRejection` runner accordingly:

```go
func TestRejection(t *testing.T) {
	t.Run("carries code, message and fields (REJECT-S1.R1)", rejectionCarriesCodeMessageFields)
	t.Run("is valid without fields (REJECT-S1.R1)", rejectionWithoutFieldsIsValid)
	t.Run("recoverable via errors.As (REJECT-S1.R2, REJECT-S4.R1)", rejectionRecoverableViaErrorsAs)
	t.Run("satisfies ServiceErrorContract", rejectionSatisfiesServiceErrorContract)
	t.Run("infrastructure error is not a rejection (REJECT-S4.R1)", infrastructureErrorIsNotARejection)
	t.Run("rejection is terminal (REJECT-S4.R1, REJECT-S4.R2)", rejectionIsTerminal)
	t.Run("decode failure is terminal (REJECT-S4.R1, REJECT-S4.R2)", decodeFailureIsTerminal)
	t.Run("infrastructure error is retryable (REJECT-S4.R1, REJECT-S4.R3)", infrastructureErrorIsRetryable)
}
```

Remove the now-unused `"encoding/json"` import from `we/rejection_test.go` if nothing else in the file uses it.

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./we/ -run TestRejection -v`
Expected: FAIL to compile — `rejection.Fields` undefined; `MakeRejection` argument type mismatch.

- [ ] **Step 3: Migrate `we/rejection.go`**

Replace the struct, constructor, and add `ToErrorFrame`:

```go
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
```

- [ ] **Step 4: Update `we/rejection_service_test.go`**

At line 62 replace:

```go
	want := MakeRejection("bump.refused", "cannot bump in this state", json.RawMessage(`{"value":0}`))
```

with:

```go
	want := MakeRejection("bump.refused", "cannot bump in this state",
		map[string]ErrorField{"value": MakeI64Field(0)})
```

At line 75 replace:

```go
	assert.JSONEq(t, string(want.Context), string(recovered.Context))
```

with:

```go
	assert.Equal(t, want.Fields, recovered.Fields)
```

Remove the `"encoding/json"` import if it is now unused in that file.

- [ ] **Step 5: Update `samples/account/handlers.go`**

Replace the withdraw guard (lines 34–40):

```go
	if cmd.Amount > state.State.Balance {
		return es.MakeRejection("account.insufficient-funds", "insufficient funds",
			map[string]es.ErrorField{
				"balance":   es.MakeI64Field(int64(state.State.Balance)),
				"requested": es.MakeI64Field(int64(cmd.Amount)),
			})
	}
```

Remove the `"encoding/json"` and `"fmt"` imports if now unused (the `json.Marshal` + encode-failure branch disappears entirely — the field model cannot fail to encode). Update `samples/account/handlers_test.go` (`TestGuardsReturnTypedRejections`) assertions from context-JSON to field lookups:

```go
		balance, ok := rejection.Fields["balance"].I64()
		require.True(t, ok)
		assert.Equal(t, int64(10), balance)
		requested, ok := rejection.Fields["requested"].I64()
		require.True(t, ok)
		assert.Equal(t, int64(25), requested)
```

(The existing scenario at `samples/account/handlers_test.go:38-44` withdraws 25 from a balance of 10 and asserts `{"balance":10,"requested":25}`; only the access pattern changes, not the scenario or the amounts.)

- [ ] **Step 6: Update `connectors/wehttp/http.go`**

Replace `rejectionBody` and `marshalRejection` (lines 106–142):

```go
// rejectionBody is the machine-readable payload returned for a domain rejection
// (REJECT-S2.R2). The context member is the rejection's fields flattened to
// plain JSON values: the tagged encoding belongs to the error-frame wire, not
// to this presentation edge (ADR-0011 decision 5).
type rejectionBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Context map[string]any `json:"context,omitempty"`
}

// flattenFields renders the closed scalar fields as plain JSON values. A
// zero-value field is a programmer error and fails the rendering (the caller
// maps that to a static 5xx rather than shipping invented content).
func flattenFields(fields map[string]we.ErrorField) (map[string]any, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	flat := make(map[string]any, len(fields))
	for name, field := range fields {
		if value, ok := field.Text(); ok {
			flat[name] = value
		} else if value, ok := field.I64(); ok {
			flat[name] = value
		} else if value, ok := field.U64(); ok {
			flat[name] = value
		} else if value, ok := field.Bool(); ok {
			flat[name] = value
		} else {
			return nil, fmt.Errorf("wehttp: rejection field %q has no value", name)
		}
	}
	return flat, nil
}

// marshalRejection renders the structured rejection body in the negotiated
// response wire (ADR-0011 decision 5) and returns the body with its media
// type. Both renderings are built from the same flattened scalar values.
func marshalRejection(rejection we.Rejection, asCBOR bool) ([]byte, string, error) {
	context, err := flattenFields(rejection.Fields)
	if err != nil {
		return nil, "", err
	}
	if !asCBOR {
		body, err := json.Marshal(rejectionBody{
			Code:    rejection.Code,
			Message: rejection.Message,
			Context: context,
		})
		return body, jsonWire, err
	}

	resource := map[string]any{
		"code":    rejection.Code,
		"message": rejection.Message,
	}
	if context != nil {
		resource["context"] = context
	}
	body, err := cbor.Marshal(resource)
	return body, cborWire, err
}
```

Confirm `fmt` is imported in `http.go` (add it if not).

- [ ] **Step 7: Update `connectors/wehttp/http_test.go`**

In `rejectionMapsToStructured4xx` (line 57) and `wrappedRejectionMapsTo4xx` (line 80) and `cborAcceptRendersRejectionBodyAsCBOR` (line 562), replace each:

```go
	rejection := we.MakeRejection("bump.refused", "cannot bump in this state", json.RawMessage(`{"value":7}`))
```

with:

```go
	rejection := we.MakeRejection("bump.refused", "cannot bump in this state",
		map[string]we.ErrorField{"value": we.MakeI64Field(7)})
```

(and correspondingly for the `"no"` message variant at line 80). The body assertions `assert.JSONEq(t, `+"`"+`{"value":7}`+"`"+`, string(body.Context))` stay as they are — flattening produces exactly that JSON.

In `rejectionMarshalFailureMapsToStatic5xx` (line 580), the malformed-JSON input is impossible under the field model; the equivalent programmer error is a zero-value field. Replace:

```go
	rejection := we.MakeRejection("x", "y", json.RawMessage("{"))
```

with:

```go
	rejection := we.MakeRejection("x", "y", map[string]we.ErrorField{"bad": {}})
```

The test's assertions (static 5xx, no detail leaked) stay unchanged. Remove the `"encoding/json"` import only if now unused (it is used elsewhere in the file — check before removing).

- [ ] **Step 8: Update `connectors/werestate/restate_test.go`**

In `TestRejectionMapsToTerminalCarryingDetail` (line 253) replace the construction:

```go
	rejection := we.MakeRejection(
		"counter.limit-exceeded",
		"increment would exceed the configured ceiling",
		map[string]we.ErrorField{"ceiling": we.MakeI64Field(100)},
	)
```

(the code/message strings are the ones the test already uses — only the third argument changes) and replace the context assertion at line 267:

```go
	ceiling, ok := recovered.Fields["ceiling"].I64()
	require.True(t, ok)
	assert.Equal(t, int64(100), ceiling)
```

- [ ] **Step 9: Run the full build and unit tests**

Run: `mise exec -- go build ./... && mise exec -- go test ./we/ ./connectors/... ./samples/... -count=1`
Expected: PASS everywhere; no remaining references to `Rejection.Context` (`rg -n "Rejection" -g '*.go' | rg "\.Context"` returns nothing).

- [ ] **Step 10: Commit**

```bash
jj split -m "Migrated Rejection from opaque JSON context to the closed scalar field model shared with wee-events.rs" we/rejection.go we/rejection_test.go we/rejection_service_test.go samples/account/handlers.go samples/account/handlers_test.go connectors/wehttp/http.go connectors/wehttp/http_test.go connectors/werestate/restate_test.go
```

---

### Task 4: werestate frame codec

**Files:**
- Create: `connectors/werestate/frame_codec.go`
- Test: `connectors/werestate/frame_codec_test.go`

**Interfaces:**
- Consumes: `we.ErrorFrame`, `we.MakeI64Field` (Tasks 1–2).
- Produces (package-private): `const errorFramePrefix = "wee-events:error-frame+json:"`; `encodeErrorFrame(we.ErrorFrame) (string, error)`; `decodeErrorFrame(string) (we.ErrorFrame, bool)`; `type framedError struct` with `Error() string` (the encoded frame) and `Unwrap() error` (the original cause).

- [ ] **Step 1: Write the failing test**

Create `connectors/werestate/frame_codec_test.go` (tests mirror wee-events.rs `crates/wee-events-restate/src/frame_codec.rs`):

```go
package werestate

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func clockDriftFrame() we.ErrorFrame {
	return we.ErrorFrame{
		Code:    "time.clock_drift_too_high",
		Message: "time server clock drift too high",
		Fields: map[string]we.ErrorField{
			"observed_ms": we.MakeI64Field(50),
			"allowed_ms":  we.MakeI64Field(10),
		},
	}
}

// Mirrors Rust error_frame_survives_restate_terminal_message_encoding.
func TestErrorFrameSurvivesTerminalMessageEncoding(t *testing.T) {
	encoded, err := encodeErrorFrame(clockDriftFrame())
	require.NoError(t, err)
	assert.Equal(t,
		`wee-events:error-frame+json:{"code":"time.clock_drift_too_high","message":"time server clock drift too high","fields":{"allowed_ms":{"I64":10},"observed_ms":{"I64":50}}}`,
		encoded)

	decoded, ok := decodeErrorFrame(encoded)
	require.True(t, ok)
	assert.Equal(t, clockDriftFrame(), decoded)
}

// Mirrors Rust legacy_display_string_is_not_a_service_error_frame.
func TestLegacyDisplayStringIsNotAFrame(t *testing.T) {
	_, ok := decodeErrorFrame("time server clock drift too high")
	assert.False(t, ok)
}

// A prefixed message whose payload is not a valid frame is not a frame either;
// it must fall to the transport lane, not decode into garbage.
func TestCorruptFramePayloadIsNotAFrame(t *testing.T) {
	_, ok := decodeErrorFrame(errorFramePrefix + `{"code":"a"}`)
	assert.False(t, ok)
}

// framedError puts the encoded frame on the wire while keeping the original
// error reachable for in-process errors.As recovery.
func TestFramedErrorCarriesFrameAndUnwrapsCause(t *testing.T) {
	rejection := we.MakeRejection("order.closed", "order is closed", nil)
	encoded, err := encodeErrorFrame(rejection.ToErrorFrame())
	require.NoError(t, err)

	framed := &framedError{message: encoded, cause: rejection}
	assert.Equal(t, encoded, framed.Error())

	var recovered we.Rejection
	require.True(t, errors.As(framed, &recovered))
	assert.Equal(t, "order.closed", recovered.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./connectors/werestate/ -run 'TestErrorFrameSurvives|TestLegacyDisplay|TestCorruptFrame|TestFramedError' -v`
Expected: FAIL to compile — `undefined: encodeErrorFrame` etc.

- [ ] **Step 3: Write minimal implementation**

Create `connectors/werestate/frame_codec.go`:

```go
package werestate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

// errorFramePrefix marks a Restate terminal-error message as carrying an
// encoded we.ErrorFrame. Restate 0.9's TerminalError has no typed payload
// channel (code + message only), so the frame rides in the message string;
// the prefix is the format discriminator — an unprefixed message is by
// definition not a frame. The constant is shared verbatim with wee-events.rs
// (crates/wee-events-restate/src/frame_codec.rs).
const errorFramePrefix = "wee-events:error-frame+json:"

// encodeErrorFrame renders a frame as a prefixed terminal-error message.
func encodeErrorFrame(frame we.ErrorFrame) (string, error) {
	data, err := json.Marshal(frame)
	if err != nil {
		return "", fmt.Errorf("werestate: encode error frame: %w", err)
	}
	return errorFramePrefix + string(data), nil
}

// decodeErrorFrame recovers a frame from a terminal-error message. The comma-ok
// result distinguishes "not a frame" (legacy or foreign message — transport
// lane) from a decoded declared error; a prefixed-but-corrupt payload is
// treated as not a frame rather than half-decoded.
func decodeErrorFrame(message string) (we.ErrorFrame, bool) {
	encoded, ok := strings.CutPrefix(message, errorFramePrefix)
	if !ok {
		return we.ErrorFrame{}, false
	}
	var frame we.ErrorFrame
	if err := json.Unmarshal([]byte(encoded), &frame); err != nil {
		return we.ErrorFrame{}, false
	}
	return frame, true
}

// framedError carries the encoded frame as its message while keeping the
// original error reachable through Unwrap: the wire sees the frame, and
// in-process callers can still recover the underlying we.Rejection via
// errors.As (RESTATE-S3.R2, RESTATE-S3.R3).
type framedError struct {
	message string
	cause   error
}

func (e *framedError) Error() string { return e.message }

func (e *framedError) Unwrap() error { return e.cause }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./connectors/werestate/ -run 'TestErrorFrameSurvives|TestLegacyDisplay|TestCorruptFrame|TestFramedError' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
jj split -m "Added the werestate error-frame codec mirroring the Rust prefix contract" connectors/werestate/frame_codec.go connectors/werestate/frame_codec_test.go
```

---

### Task 5: `mapError` emits framed rejections

**Files:**
- Modify: `connectors/werestate/restate.go:283-286` (the Rejection branch of `mapError`) and the `mapError` doc comment
- Test: `connectors/werestate/restate_test.go` (extend `TestRejectionMapsToTerminalCarryingDetail`)

**Interfaces:**
- Consumes: `encodeErrorFrame`, `framedError` (Task 4); `Rejection.ToErrorFrame` (Task 3).
- Produces: `mapError` behaviour change — a recovered `we.Rejection` maps to a terminal error whose message is the encoded frame. Classification (terminal vs retryable) is unchanged.

**Wrapping-order constraint (verified against sdk-go v0.24.0 source):** the SDK writes the OUTERMOST error's `Error()` string onto the protocol failure (`internal/restatecontext/execute_invocation.go:82` — `failure.SetMessage(err.Error())`), and `restate.TerminalError(err, code)` wraps a `CodeError` whose `Error()` prepends `"[422] "` (`internal/errors/error.go:16`). Wrapping `framedError` INSIDE `TerminalError(..., 422)` would therefore ship `"[422] wee-events:error-frame+json:…"` and break Rust's strict `strip_prefix`. The frame wrapper must be OUTERMOST: `&framedError{message: frame, cause: restate.TerminalError(err, 422)}`. All SDK introspection still works through the `Unwrap` chain: `IsTerminalError` and `ErrorCode` both use `errors.As` (`internal/errors/error.go:23-50`), so the failure ships code 422 with a clean frame message.

- [ ] **Step 1: Write the failing test**

In `connectors/werestate/restate_test.go`, extend `TestRejectionMapsToTerminalCarryingDetail` (after the existing in-process `errors.As` assertions) with the wire-shape guarantee the old test lacked:

```go
	// The cross-boundary guarantee: the terminal error's message IS the encoded
	// frame (no decoration — Rust strips the prefix strictly), so a remote
	// caller recovers code, message, and fields without any in-process error
	// chain to lean on; the 422 still rides the code channel.
	frame, ok := decodeErrorFrame(mapped.Error())
	require.True(t, ok, "terminal message must carry an encoded error frame, got %q", mapped.Error())
	assert.Equal(t, rejection.ToErrorFrame(), frame)
	assert.Equal(t, restate.Code(http.StatusUnprocessableEntity), restate.ErrorCode(mapped))
```

(`mapped` is the existing variable holding `mapError`'s result.) Add the `"net/http"` import to the test file if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./connectors/werestate/ -run TestRejectionMapsToTerminalCarryingDetail -v`
Expected: FAIL — `decodeErrorFrame(mapped.Error())` returns `ok == false` because the message is still the bare `"code: message"` display string.

- [ ] **Step 3: Write minimal implementation**

In `connectors/werestate/restate.go`, replace the Rejection branch of `mapError`:

```go
	var rejection we.Rejection
	if errors.As(err, &rejection) {
		message, encodeErr := encodeErrorFrame(rejection.ToErrorFrame())
		if encodeErr != nil {
			// A frame that cannot encode is a programmer error (a zero-value
			// field); surface it as the fault it is rather than shipping a
			// half-formed frame.
			return restate.TerminalError(fmt.Errorf("werestate: rejection frame not encodable: %w", errors.Join(encodeErr, err)), http.StatusInternalServerError)
		}
		// framedError wraps OUTSIDE the SDK terminal error: the runtime ships
		// the outermost Error() string as the failure message, and the SDK's
		// code wrapper decorates its message with "[422] ", which would break
		// the frame prefix. IsTerminalError and ErrorCode read through Unwrap.
		return &framedError{message: message, cause: restate.TerminalError(err, http.StatusUnprocessableEntity)}
	}
```

Extend the `mapError` doc comment's Rejection bullet: after "retrying a refused command cannot change the outcome.", add:

```go
//     The terminal message carries the rejection encoded as a
//     "wee-events:error-frame+json:" frame so remote callers decode the
//     declared error; the rejection value additionally stays recoverable
//     in-process through the terminal error's Unwrap chain (RESTATE-S3.R2,
//     RESTATE-S3.R3).
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./connectors/werestate/ -count=1`
Expected: PASS — including the untouched classification tests (`TestWrappedRejectionMapsToTerminal`, `TestDecodeErrorIsTerminal`, `TestRevisionConflictIsRetryable`), which prove the terminal/retryable taxonomy is unchanged.

- [ ] **Step 5: Commit**

```bash
jj split -m "Framed rejection terminal errors in the werestate connector so declared errors survive the boundary" connectors/werestate/restate.go connectors/werestate/restate_test.go
```

---

### Task 6: Typed boundary client — transport lane

**Files:**
- Create: `connectors/werestate/client.go`
- Test: `connectors/werestate/client_test.go`

**Interfaces:**
- Consumes: `EncodeKey`, `EntityResponse` (existing); `decodeErrorFrame` (Task 4) — wired in Task 7, but the call-shape lands here.
- Produces: `type Client struct`; `NewClient(baseURL string, service string, options ...ClientOption) *Client`; `type ClientOption func(*Client)`; `HTTPClient(*http.Client) ClientOption`; `(c *Client) Load(ctx context.Context, id we.AggregateId) (EntityResponse, error)`; `(c *Client) Execute(ctx context.Context, id we.AggregateId, command we.RemoteCommand) (EntityResponse, error)`; `type TransportError struct { Status int; Message string; cause error }` with `Error()`/`Unwrap()`.

Design note: the SDK's `ingress` package cannot be used here — a 422 terminal failure falls through its status switch into a flat `fmt.Errorf` string (sdk-go v0.24.0 `internal/ingress/ingress.go:187`) and its structured error types live under `internal/`. The client therefore speaks the ingress HTTP API directly: `POST {base}/{service}/{key}/{handler}`, JSON bodies, error body `{"message":..., "code":...}`.

- [ ] **Step 1: Write the failing test**

Create `connectors/werestate/client_test.go`:

```go
package werestate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func clientAggregateId(t *testing.T) we.AggregateId {
	t.Helper()
	id, err := we.MakeAggregateId("counter", "client-1")
	require.NoError(t, err)
	return id
}

// entityBody is the ingress success payload: state flattened alongside the
// $-prefixed metadata, exactly what EntityResponse.MarshalJSON produces.
func entityBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(EntityResponse{
		State:    map[string]any{"current": float64(3)},
		ID:       we.EncodedAggregateId("counter:client-1"),
		Type:     we.EntityType("counter"),
		Revision: we.Revision("00000000000000000003"),
	})
	require.NoError(t, err)
	return body
}

func TestClientLoadDecodesEntityResponse(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(entityBody(t))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	response, err := client.Load(context.Background(), clientAggregateId(t))
	require.NoError(t, err)

	assert.Equal(t, "/counter/counter:client-1/load", gotPath)
	assert.Equal(t, we.EncodedAggregateId("counter:client-1"), response.ID)
	assert.Equal(t, map[string]any{"current": float64(3)}, response.State)
}

func TestClientExecutePostsCommandAndDecodesResponse(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(entityBody(t))
	}))
	defer server.Close()

	command := we.RemoteCommand{
		CommandName: "counter:increment",
		Payload:     we.Data{Encoding: "application/json", Data: []byte(`{"amount":1}`)},
	}

	client := NewClient(server.URL, "counter")
	response, err := client.Execute(context.Background(), clientAggregateId(t), command)
	require.NoError(t, err)

	var sent we.RemoteCommand
	require.NoError(t, json.Unmarshal(gotBody, &sent))
	assert.Equal(t, command.CommandName, sent.CommandName)
	assert.Equal(t, we.Revision("00000000000000000003"), response.Revision)
}

// A failure to reach the ingress at all is a transport failure with no status.
func TestClientConnectionFailureIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close() // deliberately dead

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
	assert.Equal(t, 0, transport.Status)
}

// A non-2xx response without a frame is a transport failure carrying the
// ingress status and message.
func TestClientPlainFailureIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"store is down","code":500}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
	assert.Equal(t, http.StatusInternalServerError, transport.Status)
	assert.Contains(t, transport.Message, "store is down")
}

// An undecodable success body is a transport failure too: the service answered
// but the boundary mangled it — never a declared outcome.
func TestClientUndecodableSuccessBodyIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"no":"metadata"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./connectors/werestate/ -run TestClient -v`
Expected: FAIL to compile — `undefined: NewClient`, `undefined: TransportError`.

- [ ] **Step 3: Write minimal implementation**

Create `connectors/werestate/client.go`:

```go
package werestate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

// TransportError is a boundary-lane failure: the call never produced a
// declared service outcome. Network loss, an unreachable ingress, a non-frame
// failure body, or an undecodable response all land here. It is deliberately a
// distinct type from any declared error so callers branch with errors.As and
// transport concerns are never folded into a service's error contract (the
// Declared-vs-Transport separation from the Rust execution-model addendum,
// rendered as plain Go error types).
type TransportError struct {
	// Status is the ingress HTTP status; 0 when the request never completed.
	Status int
	// Message is the transport-level failure detail.
	Message string
	cause   error
}

func (e *TransportError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("werestate: transport failure (status %d): %s", e.Status, e.Message)
	}
	return "werestate: transport failure: " + e.Message
}

func (e *TransportError) Unwrap() error { return e.cause }

// FrameDecoder maps a decoded error frame to a service-specific declared
// error. It returns ok=false to pass the frame to the next decoder; a frame no
// decoder claims falls back to the generic we.Rejection carrying the frame's
// code, message, and fields.
type FrameDecoder func(we.ErrorFrame) (error, bool)

// Client is the typed boundary handle for a werestate service reached through
// Restate ingress. It speaks the ingress HTTP API directly because the SDK's
// ingress client flattens terminal failures into opaque strings; owning the
// HTTP exchange is what lets declared errors and transport failures stay in
// separate lanes.
type Client struct {
	baseURL  string
	service  string
	http     *http.Client
	decoders []FrameDecoder
}

// ClientOption configures a Client before first use.
type ClientOption func(*Client)

// HTTPClient overrides the HTTP client used for ingress calls.
func HTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		c.http = client
	}
}

// Decoder appends a FrameDecoder consulted, in registration order, before the
// generic rejection fallback.
func Decoder(decoder FrameDecoder) ClientOption {
	return func(c *Client) {
		c.decoders = append(c.decoders, decoder)
	}
}

// NewClient builds a boundary client for one service registered with the
// Restate runtime at baseURL (the ingress address, e.g. "http://localhost:8080").
func NewClient(baseURL string, service string, options ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		service: service,
		http:    http.DefaultClient,
	}
	for _, option := range options {
		option(c)
	}
	if c.http == nil {
		c.http = http.DefaultClient
	}
	return c
}

// Load reads current entity state through the service's load handler.
func (c *Client) Load(ctx context.Context, id we.AggregateId) (EntityResponse, error) {
	return c.call(ctx, id, "load", nil)
}

// Execute dispatches a command through the service's execute handler.
func (c *Client) Execute(ctx context.Context, id we.AggregateId, command we.RemoteCommand) (EntityResponse, error) {
	body, err := json.Marshal(command)
	if err != nil {
		return EntityResponse{}, fmt.Errorf("werestate: encode remote command: %w", err)
	}
	return c.call(ctx, id, "execute", body)
}

// call performs one ingress exchange: POST {base}/{service}/{key}/{handler}.
func (c *Client) call(ctx context.Context, id we.AggregateId, handler string, body []byte) (EntityResponse, error) {
	key, err := EncodeKey(id)
	if err != nil {
		return EntityResponse{}, fmt.Errorf("werestate: invalid aggregate id: %w", err)
	}

	target := c.baseURL + "/" + url.PathEscape(c.service) + "/" + url.PathEscape(key) + "/" + url.PathEscape(handler)
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, reader)
	if err != nil {
		return EntityResponse{}, fmt.Errorf("werestate: build ingress request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return EntityResponse{}, &TransportError{Message: err.Error(), cause: err}
	}
	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(response.Body)
	if err != nil {
		return EntityResponse{}, &TransportError{Status: response.StatusCode, Message: "unreadable response body: " + err.Error(), cause: err}
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return EntityResponse{}, c.classifyFailure(response.StatusCode, data)
	}

	var entity EntityResponse
	if err := json.Unmarshal(data, &entity); err != nil {
		return EntityResponse{}, &TransportError{Status: response.StatusCode, Message: "undecodable entity response: " + err.Error(), cause: err}
	}
	return entity, nil
}

// classifyFailure separates the two failure lanes: a failure body whose
// message carries an encoded error frame is a DECLARED service error and is
// decoded back into a branchable error value; anything else is a
// *TransportError. A frame always becomes a declared error — at minimum the
// generic we.Rejection — never a transport failure.
func (c *Client) classifyFailure(status int, body []byte) error {
	var failure struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	}
	if err := json.Unmarshal(body, &failure); err != nil {
		return &TransportError{Status: status, Message: string(body)}
	}

	frame, ok := decodeErrorFrame(failure.Message)
	if !ok {
		return &TransportError{Status: status, Message: failure.Message}
	}

	for _, decode := range c.decoders {
		if declared, claimed := decode(frame); claimed {
			return declared
		}
	}
	return we.Rejection{Code: frame.Code, Message: frame.Message, Fields: frame.Fields}
}
```

Note: `classifyFailure`'s declared-error path is exercised in Task 7; in this task only the transport branches are covered. Both land now because splitting the function across tasks would leave Task 6 committing dead code paths *without their tests* — acceptable here since Task 7 follows immediately in the same plan.

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./connectors/werestate/ -run TestClient -v`
Expected: PASS (five tests)

- [ ] **Step 5: Commit**

```bash
jj split -m "Added the typed werestate boundary client with a distinct transport error lane" connectors/werestate/client.go connectors/werestate/client_test.go
```

---

### Task 7: Client declared-error lane

**Files:**
- Test: `connectors/werestate/client_test.go` (append)
- Modify: `connectors/werestate/client.go` (only if a test exposes a gap — the lane logic landed in Task 6)

**Interfaces:**
- Consumes: `encodeErrorFrame` (Task 4), `we.Rejection.ToErrorFrame` (Task 3), `Decoder`/`FrameDecoder` (Task 6).
- Produces: verified behaviour — framed failures surface as `we.Rejection` (or a custom-decoded declared error) via `errors.As`, with fields intact.

- [ ] **Step 1: Write the failing (or verifying) tests**

Append to `connectors/werestate/client_test.go`:

```go
// framedFailureBody builds the exact ingress failure body the werestate server
// produces: mapError encodes the rejection's frame into the terminal message,
// and the ingress renders {"message": <terminal message>, "code": <status>}.
func framedFailureBody(t *testing.T, rejection we.Rejection, status int) []byte {
	t.Helper()
	message, err := encodeErrorFrame(rejection.ToErrorFrame())
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{"message": message, "code": status})
	require.NoError(t, err)
	return body
}

// The declared lane: a framed 422 decodes back into a branchable we.Rejection
// with its fields intact — the same value a caller would see in-process.
func TestClientDecodesFramedRejection(t *testing.T) {
	rejection := we.MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]we.ErrorField{
			"balance":   we.MakeI64Field(0),
			"requested": we.MakeI64Field(100),
		})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(framedFailureBody(t, rejection, http.StatusUnprocessableEntity))
	}))
	defer server.Close()

	client := NewClient(server.URL, "account")
	_, err := client.Execute(context.Background(), clientAggregateId(t), we.RemoteCommand{})

	var recovered we.Rejection
	require.True(t, errors.As(err, &recovered), "expected we.Rejection, got %T: %v", err, err)
	assert.Equal(t, rejection, recovered)

	var transport *TransportError
	assert.False(t, errors.As(err, &transport), "a declared error must never read as a transport failure")
}

// insufficientFundsError is a service-specific declared error a caller might
// define; the decoder test proves callers can branch on their own types via
// errors.As rather than the generic rejection.
type insufficientFundsError struct {
	Balance   int64
	Requested int64
}

func (e *insufficientFundsError) Error() string {
	return "insufficient funds"
}

// A registered FrameDecoder claims frames it recognises, so callers branch on
// their own declared error types rather than the generic rejection.
func TestClientCustomDecoderClaimsFrame(t *testing.T) {
	rejection := we.MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]we.ErrorField{
			"balance":   we.MakeI64Field(25),
			"requested": we.MakeI64Field(100),
		})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(framedFailureBody(t, rejection, http.StatusUnprocessableEntity))
	}))
	defer server.Close()

	client := NewClient(server.URL, "account", Decoder(func(frame we.ErrorFrame) (error, bool) {
		if frame.Code != "account.insufficient-funds" {
			return nil, false
		}
		balance, _ := frame.Fields["balance"].I64()
		requested, _ := frame.Fields["requested"].I64()
		return &insufficientFundsError{Balance: balance, Requested: requested}, true
	}))

	_, err := client.Execute(context.Background(), clientAggregateId(t), we.RemoteCommand{})

	var declared *insufficientFundsError
	require.True(t, errors.As(err, &declared), "expected *insufficientFundsError, got %T: %v", err, err)
	assert.Equal(t, int64(25), declared.Balance)
	assert.Equal(t, int64(100), declared.Requested)
}

// A decoder that declines passes through to the generic rejection fallback.
func TestClientUnclaimedFrameFallsBackToRejection(t *testing.T) {
	rejection := we.MakeRejection("order.closed", "order is closed", nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(framedFailureBody(t, rejection, http.StatusUnprocessableEntity))
	}))
	defer server.Close()

	client := NewClient(server.URL, "order", Decoder(func(we.ErrorFrame) (error, bool) {
		return nil, false
	}))

	_, err := client.Execute(context.Background(), clientAggregateId(t), we.RemoteCommand{})

	var recovered we.Rejection
	require.True(t, errors.As(err, &recovered))
	assert.Equal(t, "order.closed", recovered.Code)
}
```

- [ ] **Step 2: Run the tests**

Run: `mise exec -- go test ./connectors/werestate/ -run TestClient -v`
Expected: PASS — the lane logic landed in Task 6; these tests verify it against the exact bytes the server side produces (`framedFailureBody` reuses `encodeErrorFrame`, so server and client are tested against one codec, not two copies). If any test fails, fix `classifyFailure` in `client.go` until green.

- [ ] **Step 3: Commit**

```bash
jj split -m "Covered the boundary client's declared-error lane including custom frame decoders" connectors/werestate/client_test.go
```

---

### Task 8: Cross-boundary integration round-trip

**Files:**
- Modify: `connectors/werestate/integration_test.go` (build tag `integration`)

**Interfaces:**
- Consumes: `NewClient`, `TransportError` (Task 6); `account.Service` (`samples/account/loader.go:15`); the existing `startEnvironment`/`startRestateRuntime` harness.
- Produces: proof that a rejection raised in a handler crosses a REAL Restate runtime and decodes back into a branchable `we.Rejection` with fields intact — closing the caveat recorded in the 2026-07-08 conformance review (in-process-only coverage).

- [ ] **Step 1: Extend the harness to expose the ingress URL and bind the account service**

In `integration_test.go`:
- Change `startRestateRuntime` to also return the ingress base URL: it already computes `mappedIngress`; return `fmt.Sprintf("http://localhost:%s", mappedIngress.Port())` alongside the `*ingress.Client`.
- Change `startEnvironment`'s signature to `func startEnvironment(t *testing.T) (*ingress.Client, string, *memoryStore)` and thread the URL through. Update the three existing call sites (`client, store := startEnvironment(t)` → `client, _, store := startEnvironment(t)` etc.).
- Where the Restate server binds the counter service (`restateSrv := server.NewRestate().Bind(svc.Definition(serviceName))`), additionally bind the account sample:

```go
	accountSvc := NewService(account.Service(store))
	restateSrv := server.NewRestate().
		Bind(svc.Definition(serviceName)).
		Bind(accountSvc.Definition("account"))
```

Add the import `"github.com/weegigs/wee-events-go/samples/account"`.

- [ ] **Step 2: Write the round-trip test**

Append to `integration_test.go`:

```go
// remoteCommand JSON-encodes a typed command into the RemoteCommand envelope.
func remoteCommand(t *testing.T, command any) we.RemoteCommand {
	t.Helper()
	payload, err := json.Marshal(command)
	require.NoError(t, err)
	return we.RemoteCommand{
		CommandName: we.CommandNameOf(command),
		Payload:     we.Data{Encoding: "application/json", Data: payload},
	}
}

// The full conformance loop for the error-frame contract: a domain rejection
// raised inside a handler crosses a real Restate runtime — mapError encodes
// the frame into the terminal message, the ingress carries it, and the typed
// boundary client decodes it back into a branchable we.Rejection with its
// fields intact. This is the cross-boundary coverage the 2026-07-08
// conformance review flagged as missing.
func TestRejectionRoundTripsAcrossBoundary(t *testing.T) {
	_, ingressURL, _ := startEnvironment(t)

	id, err := we.MakeAggregateId("account", "boundary-1")
	require.NoError(t, err)

	client := NewClient(ingressURL, "account")
	ctx := context.Background()

	_, err = client.Execute(ctx, id, remoteCommand(t, account.Open{Owner: "kevin"}))
	require.NoError(t, err, "opening the account must succeed")

	loaded, err := client.Load(ctx, id)
	require.NoError(t, err, "the typed client's load path must work against real ingress")
	assert.Equal(t, we.EncodedAggregateId("account:boundary-1"), loaded.ID)

	_, err = client.Execute(ctx, id, remoteCommand(t, account.Withdraw{Amount: 100}))

	var rejection we.Rejection
	require.True(t, errors.As(err, &rejection), "expected we.Rejection, got %T: %v", err, err)
	assert.Equal(t, "account.insufficient-funds", rejection.Code)

	balance, ok := rejection.Fields["balance"].I64()
	require.True(t, ok, "rejection must carry the balance field across the boundary")
	assert.Equal(t, int64(0), balance)

	requested, ok := rejection.Fields["requested"].I64()
	require.True(t, ok, "rejection must carry the requested field across the boundary")
	assert.Equal(t, int64(100), requested)

	var transport *TransportError
	assert.False(t, errors.As(err, &transport), "a declared rejection must not classify as transport")
}
```

Add the `"errors"` import if not already present in the file.

- [ ] **Step 3: Run the integration suite (Docker required)**

Run: `mise exec -- go test -tags integration -v -count=1 -timeout 600s ./connectors/werestate/`
Expected: PASS — all pre-existing integration tests plus `TestRejectionRoundTripsAcrossBoundary`. If Docker is unavailable, STOP and report; do not mark this task complete on unit tests alone.

- [ ] **Step 4: Commit**

```bash
jj split -m "Proved the rejection error frame round-trips a real Restate boundary with fields intact" connectors/werestate/integration_test.go
```

---

### Task 9: Spec/document sync and quality gates

**Files:**
- Modify: `documents/features/05-rejection-error-taxonomy.md`
- Modify: `documents/features/09-error-surfacing.md`
- Modify: `connectors/werestate/restate.go:1-16` (package doc)

- [ ] **Step 1: Update the rejection taxonomy spec**

In `documents/features/05-rejection-error-taxonomy.md`, update every statement of the Rejection shape from `code`/`message`/`context` (raw JSON) to `code`/`message`/`fields` (closed scalar set). Specifically:
- Lines 16, 34, 39, 45, 59, 167, 170: replace "structured `context`" / "`context`" with "structured `fields` (closed scalar set: Text, I64, U64, Bool)" / "`fields`", keeping each sentence's surrounding wording.
- Line 113 (the Rust sketch `pub context: serde_json::Value,`): replace with `pub fields: BTreeMap<FieldName, ErrorField>,` and a note that the field model is the option A decision (2026-07-09), shared with wee-events.rs.
- Line 131: replace the clause "`Context json.RawMessage` (raw JSON so callers get machine-readable detail, matching …)" with "`Fields map[string]ErrorField` (the closed scalar field model — flat scalars, never opaque JSON — so detail stays branchable and lossless across implementations; option A decision, 2026-07-09)".
- Line 145: the HTTP body wording stays `code`/`message`/`context` — add "(`context` is the fields flattened to plain JSON values; the tagged encoding belongs to the error-frame wire)".
- Add one sentence under the spec's boundary-mapping section: "The Restate connector encodes a recovered Rejection as a `wee-events:error-frame+json:` frame in the terminal error message so remote callers decode the declared error (see wee-events.rs `documents/plans/2026-06-22-restate-service-error-contract-design.md`)."

- [ ] **Step 2: Update the error-surfacing spec**

In `documents/features/09-error-surfacing.md` line 45, replace "`context` shall carry `{"balance": …, "requested": …}`" with "`fields` shall carry `balance` and `requested` as I64 fields (rendered as plain values in the HTTP body's `context`)". Update the line 132 verification wording from "context fields" to "typed fields".

- [ ] **Step 3: Extend the werestate package doc**

In `connectors/werestate/restate.go`, append to the package comment (after the addressing paragraph):

```go
// Errors: a recovered we.Rejection crosses the boundary as a
// "wee-events:error-frame+json:" frame in the terminal error message (Restate
// 0.9 has no typed error payload channel), byte-compatible with wee-events.rs.
// The typed boundary client (client.go) decodes frames back into declared
// errors and keeps transport failures in a distinct *TransportError lane.
```

- [ ] **Step 4: Run the full quality gates**

Run: `just lint && just test`
Expected: both PASS clean — no lint suppressions anywhere in the diff (`rg -n "nolint" -g '*.go'` over changed files returns nothing).

- [ ] **Step 5: Commit**

```bash
jj split -m "Synchronised the rejection taxonomy and error-surfacing specs with the closed field model and frame contract" documents/features/05-rejection-error-taxonomy.md documents/features/09-error-surfacing.md connectors/werestate/restate.go
```

- [ ] **Step 6: Close out**

- `bd close wee-events-go-tu2 --reason="Error-frame wire contract implemented: frame types in we/, prefix codec + framed mapError in werestate, typed boundary client with Declared-vs-Transport lanes, cross-boundary integration round-trip green."`
- Report handoff per the conservative profile: changed files, validation output, proposed commits. Do not push.

---

## Verification Notes for the Final Review

- **Interop invariant:** `we/error_frame_test.go`'s `rustFrameVector` string must equal, byte for byte, what `serde_json::to_string` produces in wee-events.rs `frame_codec.rs::error_frame_survives_restate_terminal_message_encoding` (same frame, same ordering). If the assertion needs "fixing" to pass, the codec is wrong — fix the codec, never the vector.
- **No dropped detail:** `rg -n "\.Context" -g '*.go'` must return no `Rejection`-related hits after Task 3.
- **Lane separation:** no code path may convert a decodable frame into a `*TransportError`, and no transport failure may surface as a `we.Rejection`.
- **Honest coverage:** Task 8's integration test is the only proof the contract survives a real boundary; unit green without it is not "done" (superpowers:verification-before-completion).
