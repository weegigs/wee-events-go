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
