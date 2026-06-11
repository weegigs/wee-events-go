package we

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// goldenJSONPayload and goldenBinaryPayload are the fixed payloads behind the
// golden literals: one canonical JSON document and one CBOR document
// ({"v": 7}, whose base64 spelling is "oWF2Bw==").
var (
	goldenJSONPayload   = []byte(`{"name":"widget","count":7}`)
	goldenBinaryPayload = []byte{0xa1, 0x61, 0x76, 0x07}
)

// TestDataJSONSpelling pins the JSON-medium spelling of the presentation
// contract (ADR-0011 decision 5): both matrix cells as golden literals, the
// refusals, and the zero value.
func TestDataJSONSpelling(t *testing.T) {
	// JSON spelling × JSON payload — payload bytes embed verbatim as raw JSON.
	t.Run("json payload bytes embed as raw json", func(t *testing.T) {
		data := Data{Encoding: JSONEncoding, Data: goldenJSONPayload}

		spelled, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Equal(t, `{"encoding":"application/json","data":{"name":"widget","count":7}}`, string(spelled))

		var parsed Data
		require.NoError(t, json.Unmarshal(spelled, &parsed))
		assert.Equal(t, data, parsed)
	})

	// JSON spelling × non-canonical JSON payload — interior whitespace and raw
	// HTML characters embed untouched. Pinned on MarshalJSON's own output,
	// where the spelling is defined: an outer document marshalled with
	// encoding/json re-encodes nested Marshaler output (eliding insignificant
	// whitespace and escaping HTML), which is the outer encoder's behaviour,
	// not this spelling's.
	t.Run("non-canonical json payload bytes embed untouched", func(t *testing.T) {
		data := Data{Encoding: JSONEncoding, Data: []byte(`{ "tag" : "<b> & </b>" }`)}

		spelled, err := data.MarshalJSON()
		require.NoError(t, err)
		assert.Equal(t, `{"encoding":"application/json","data":{ "tag" : "<b> & </b>" }}`, string(spelled))

		var parsed Data
		require.NoError(t, json.Unmarshal(spelled, &parsed))
		assert.Equal(t, data, parsed)
	})

	// JSON spelling × binary payload — base64 is the JSON medium's only total
	// spelling of binary.
	t.Run("binary payload bytes spell as base64", func(t *testing.T) {
		data := Data{Encoding: CBOREncoding, Data: goldenBinaryPayload}

		spelled, err := json.Marshal(data)
		require.NoError(t, err)
		assert.Equal(t, `{"encoding":"application/cbor","data":"oWF2Bw=="}`, string(spelled))

		var parsed Data
		require.NoError(t, json.Unmarshal(spelled, &parsed))
		assert.Equal(t, data, parsed)
	})

	// A tagged-JSON payload whose bytes are not JSON is corrupt: the spelling
	// refuses it rather than guessing a representation.
	t.Run("json-tagged non-json bytes refuse to marshal", func(t *testing.T) {
		_, err := json.Marshal(Data{Encoding: JSONEncoding, Data: []byte("not json")})
		require.Error(t, err)
		assert.ErrorContains(t, err, "not valid JSON")
	})

	// A tagged-JSON payload with no bytes at all has no JSON value to embed.
	t.Run("json-tagged nil bytes refuse to marshal", func(t *testing.T) {
		_, err := json.Marshal(Data{Encoding: JSONEncoding, Data: nil})
		require.Error(t, err)
		assert.ErrorContains(t, err, "not valid JSON")
	})

	// Whitespace surrounding the value cannot survive any JSON reader's
	// capture of the "data" value, so such bytes have no verbatim spelling.
	t.Run("json-tagged bytes with surrounding whitespace refuse to marshal", func(t *testing.T) {
		_, err := json.Marshal(Data{Encoding: JSONEncoding, Data: []byte(` {"a":1} `)})
		require.Error(t, err)
		assert.ErrorContains(t, err, "leading or trailing whitespace")
	})

	// The zero value takes the binary branch (empty tag), where nil bytes
	// spell null, and round-trips to itself.
	t.Run("zero value spells as null data and round-trips", func(t *testing.T) {
		spelled, err := json.Marshal(Data{})
		require.NoError(t, err)
		assert.Equal(t, `{"encoding":"","data":null}`, string(spelled))

		var parsed Data
		require.NoError(t, json.Unmarshal(spelled, &parsed))
		assert.Equal(t, Data{}, parsed)
	})
}

// TestDataCBORSpelling pins the CBOR-medium spelling: every payload encoding
// rides as a native byte string under the same field names as the JSON
// spelling, and base64 never appears.
func TestDataCBORSpelling(t *testing.T) {
	// CBOR spelling × JSON payload.
	t.Run("json payload bytes ride as a native byte string", func(t *testing.T) {
		data := Data{Encoding: JSONEncoding, Data: goldenJSONPayload}

		spelled, err := cbor.Marshal(data)
		require.NoError(t, err)
		assert.True(t, bytes.Contains(spelled, goldenJSONPayload), "payload bytes must appear verbatim as byte-string content")

		var parsed Data
		require.NoError(t, cbor.Unmarshal(spelled, &parsed))
		assert.Equal(t, data, parsed)
	})

	// CBOR spelling × binary payload.
	t.Run("binary payload bytes ride as a native byte string, never base64", func(t *testing.T) {
		data := Data{Encoding: CBOREncoding, Data: goldenBinaryPayload}

		spelled, err := cbor.Marshal(data)
		require.NoError(t, err)
		assert.True(t, bytes.Contains(spelled, goldenBinaryPayload), "payload bytes must appear verbatim as byte-string content")
		assert.False(t, bytes.Contains(spelled, []byte("oWF2Bw==")), "the CBOR medium must not smuggle a base64 spelling")

		var parsed Data
		require.NoError(t, cbor.Unmarshal(spelled, &parsed))
		assert.Equal(t, data, parsed)
	})

	// The CBOR spelling uses the same field names as the JSON spelling, so the
	// envelope shape is medium-independent.
	t.Run("field names match the json spelling", func(t *testing.T) {
		spelled, err := cbor.Marshal(Data{Encoding: CBOREncoding, Data: []byte{0x01}})
		require.NoError(t, err)

		var fields map[string]any
		require.NoError(t, cbor.Unmarshal(spelled, &fields))
		assert.Contains(t, fields, "encoding")
		assert.Contains(t, fields, "data")
		assert.Len(t, fields, 2)
	})
}

// nonCanonicalJSONPayloadGen generates JSON value bytes in producer styles
// the Go encoder never emits — interior whitespace between tokens, raw <, >,
// & and unicode inside strings (serde_json leaves them unescaped) — assembled
// as text, never passed through a JSON marshaller. Values carry no leading or
// trailing whitespace, which the spelling refuses by design.
func nonCanonicalJSONPayloadGen() *rapid.Generator[[]byte] {
	return rapid.Custom(func(rt *rapid.T) []byte {
		return []byte(drawJSONValue(rt, 0))
	})
}

func drawInteriorWhitespace(rt *rapid.T) string {
	return rapid.SampledFrom([]string{"", " ", "  ", "\t", "\n "}).Draw(rt, "whitespace")
}

func drawJSONStringLiteral(rt *rapid.T) string {
	pieces := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "Z", "0", "<", ">", "&", " ", "ü", "界"}), 0, 6).Draw(rt, "string pieces")
	return `"` + strings.Join(pieces, "") + `"`
}

func drawJSONValue(rt *rapid.T, depth int) string {
	limit := 5
	if depth >= 2 {
		limit = 3
	}

	switch rapid.IntRange(0, limit).Draw(rt, "kind") {
	case 0:
		return "null"
	case 1:
		return "true"
	case 2:
		return strconv.FormatInt(rapid.Int64().Draw(rt, "number"), 10)
	case 3:
		return drawJSONStringLiteral(rt)
	case 4:
		elements := make([]string, rapid.IntRange(0, 3).Draw(rt, "array length"))
		for i := range elements {
			elements[i] = drawInteriorWhitespace(rt) + drawJSONValue(rt, depth+1) + drawInteriorWhitespace(rt)
		}
		return "[" + strings.Join(elements, ",") + "]"
	default:
		members := make([]string, rapid.IntRange(0, 3).Draw(rt, "object length"))
		for i := range members {
			members[i] = drawInteriorWhitespace(rt) + fmt.Sprintf("%q", fmt.Sprintf("k%d", i)) +
				drawInteriorWhitespace(rt) + ":" + drawInteriorWhitespace(rt) +
				drawJSONValue(rt, depth+1) + drawInteriorWhitespace(rt)
		}
		return "{" + strings.Join(members, ",") + "}"
	}
}

// TestDataSpellingProperty — generative totality (rapid, ADR-0009): for any
// tagged payload (non-canonical JSON value bytes under JSONEncoding,
// arbitrary bytes under CBOREncoding), spell→parse is identity — verbatim
// payload bytes and the original tag — in both mediums. The JSON medium is
// exercised through MarshalJSON's own output, where the spelling is defined:
// an outer document marshalled with encoding/json re-encodes any nested
// Marshaler output (eliding insignificant whitespace and escaping HTML),
// which is the outer encoder's behaviour, not this spelling's.
func TestDataSpellingProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		encoding := rapid.SampledFrom([]string{JSONEncoding, CBOREncoding}).Draw(rt, "encoding")
		var payload []byte
		if encoding == JSONEncoding {
			payload = nonCanonicalJSONPayloadGen().Draw(rt, "json payload")
		} else {
			payload = rapid.SliceOf(rapid.Byte()).Draw(rt, "binary payload")
		}
		data := Data{Encoding: encoding, Data: payload}

		jsonSpelled, err := data.MarshalJSON()
		require.NoError(rt, err)
		require.True(rt, json.Valid(jsonSpelled), "the spelling must be a valid JSON document")
		var fromJSON Data
		require.NoError(rt, json.Unmarshal(jsonSpelled, &fromJSON))
		require.Equal(rt, data.Encoding, fromJSON.Encoding)
		require.True(rt, bytes.Equal(data.Data, fromJSON.Data), "json medium must round-trip payload bytes verbatim")

		cborSpelled, err := cbor.Marshal(data)
		require.NoError(rt, err)
		var fromCBOR Data
		require.NoError(rt, cbor.Unmarshal(cborSpelled, &fromCBOR))
		require.Equal(rt, data.Encoding, fromCBOR.Encoding)
		require.True(rt, bytes.Equal(data.Data, fromCBOR.Data), "cbor medium must round-trip payload bytes verbatim")
	})
}
