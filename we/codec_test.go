package we

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type codecPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// CBOR-S3.R3 - the discriminator strings must match the wee-events.rs sibling
// byte-for-byte.
func discriminatorStringsMatchRust(t *testing.T) {
	assert.Equal(t, "application/json", JSONEncoding)
	assert.Equal(t, "application/cbor", CBOREncoding)
	assert.Equal(t, "application/json", MakeJSONEncoder().Encoding())
	assert.Equal(t, "application/json", MakeJSONDecoder().Encoding())
	assert.Equal(t, "application/cbor", MakeCBOREncoder().Encoding())
	assert.Equal(t, "application/cbor", MakeCBORDecoder().Encoding())
}

// CBOR-S1.R1, CBOR-S1.R2, CBOR-S3.R2 - round-trip through both codecs; the
// decoded value equals the original and Data.Encoding is correct for each.
func roundTripsThroughJSONAndCBOR(t *testing.T) {
	original := codecPayload{Name: "widget", Count: 7}

	t.Run("json", func(t *testing.T) {
		encoded, err := MakeJSONEncoder().Encode(original)
		require.NoError(t, err)
		assert.Equal(t, JSONEncoding, encoded.Encoding)

		var decoded codecPayload
		require.NoError(t, MakeJSONDecoder().Decode(encoded, &decoded))
		assert.Equal(t, original, decoded)
	})

	t.Run("cbor", func(t *testing.T) {
		encoded, err := MakeCBOREncoder().Encode(original)
		require.NoError(t, err)
		assert.Equal(t, CBOREncoding, encoded.Encoding)

		var decoded codecPayload
		require.NoError(t, MakeCBORDecoder().Decode(encoded, &decoded))
		assert.Equal(t, original, decoded)
	})
}

// CBOR-S3.R2 - a payload stored as application/json decodes byte-for-byte
// equivalent to the pre-feature behaviour (json.Marshal output identical).
func jsonEncoderPreservesPreFeatureBytes(t *testing.T) {
	original := codecPayload{Name: "widget", Count: 7}

	expected, err := json.Marshal(original)
	require.NoError(t, err)

	encoded, err := MakeJSONEncoder().Encode(original)
	require.NoError(t, err)

	assert.Equal(t, JSONEncoding, encoded.Encoding)
	assert.Equal(t, json.RawMessage(expected), encoded.Data)
}

// CBOR-S1.R3 - a value that cannot be CBOR-encoded yields a typed encode error
// and no fallback to another encoding.
func cborEncodeFailureReturnsErrorNoFallback(t *testing.T) {
	// channels cannot be marshalled by cbor (or json).
	encoded, err := MakeCBOREncoder().Encode(make(chan int))

	require.Error(t, err)
	// No fallback: nothing was produced, and the error originates from cbor.
	assert.Equal(t, Data{}, encoded)
	assert.Empty(t, encoded.Encoding)

	var unsupported *cbor.UnsupportedTypeError
	assert.True(t, errors.As(err, &unsupported), "expected a cbor unsupported-type error, got %T", err)
}

// CBOR-S2.R1 - Decoders selects a decoder by the envelope's encoding field.
func decodersSelectByEncoding(t *testing.T) {
	decoders := MakeDecoders(MakeJSONDecoder(), MakeCBORDecoder())
	original := codecPayload{Name: "widget", Count: 7}

	jsonData, err := MakeJSONEncoder().Encode(original)
	require.NoError(t, err)
	cborData, err := MakeCBOREncoder().Encode(original)
	require.NoError(t, err)

	var fromJSON codecPayload
	require.NoError(t, decoders.Decode(jsonData, &fromJSON))
	assert.Equal(t, original, fromJSON)

	var fromCBOR codecPayload
	require.NoError(t, decoders.Decode(cborData, &fromCBOR))
	assert.Equal(t, original, fromCBOR)
}

// CBOR-S2.R2 - while both decoders are registered, each event decodes with the
// matching decoder regardless of order in a mixed-encoding stream.
func decodersHandleMixedEncodingStream(t *testing.T) {
	decoders := MakeDecoders(MakeJSONDecoder(), MakeCBORDecoder())

	originals := []codecPayload{
		{Name: "first", Count: 1},
		{Name: "second", Count: 2},
		{Name: "third", Count: 3},
		{Name: "fourth", Count: 4},
	}

	// Mixed stream: cbor, json, cbor, json (order is not json-then-cbor).
	stream := make([]Data, 0, len(originals))
	for i, original := range originals {
		var (
			encoded Data
			err     error
		)
		if i%2 == 0 {
			encoded, err = MakeCBOREncoder().Encode(original)
		} else {
			encoded, err = MakeJSONEncoder().Encode(original)
		}
		require.NoError(t, err)
		stream = append(stream, encoded)
	}

	for i, data := range stream {
		var decoded codecPayload
		require.NoError(t, decoders.Decode(data, &decoded))
		assert.Equal(t, originals[i], decoded)
	}
}

// CBOR-S2.R3 - an envelope whose encoding names no registered decoder yields a
// typed unknown-encoding error, no panic, and no default decode.
func decodersRejectUnknownEncoding(t *testing.T) {
	decoders := MakeDecoders(MakeJSONDecoder(), MakeCBORDecoder())

	var decoded codecPayload
	err := decoders.Decode(Data{Encoding: "application/x-unknown", Data: []byte("{}")}, &decoded)

	require.Error(t, err)
	var unknown *UnknownEncodingError
	require.True(t, errors.As(err, &unknown), "expected *UnknownEncodingError, got %T", err)
	assert.Equal(t, "application/x-unknown", unknown.Actual)
	// No default decode happened: the target is untouched.
	assert.Equal(t, codecPayload{}, decoded)
}

// A decoder invoked directly with an envelope it does not own reports the
// mismatch as a typed *InvalidEncodingError carrying both encodings — distinct
// from the registry's *UnknownEncodingError ("no decoder at all").
func directDecoderRejectsMismatchedEncoding(t *testing.T) {
	jsonData, err := MakeJSONEncoder().Encode(codecPayload{Name: "widget", Count: 7})
	require.NoError(t, err)
	cborData, err := MakeCBOREncoder().Encode(codecPayload{Name: "widget", Count: 7})
	require.NoError(t, err)

	t.Run("json decoder rejects cbor envelope", func(t *testing.T) {
		var decoded codecPayload
		err := MakeJSONDecoder().Decode(cborData, &decoded)
		require.Error(t, err)
		var invalid *InvalidEncodingError
		require.True(t, errors.As(err, &invalid), "expected *InvalidEncodingError, got %T", err)
		assert.Equal(t, JSONEncoding, invalid.Expected)
		assert.Equal(t, CBOREncoding, invalid.Actual)
		assert.Equal(t, codecPayload{}, decoded)
	})

	t.Run("cbor decoder rejects json envelope", func(t *testing.T) {
		var decoded codecPayload
		err := MakeCBORDecoder().Decode(jsonData, &decoded)
		require.Error(t, err)
		var invalid *InvalidEncodingError
		require.True(t, errors.As(err, &invalid), "expected *InvalidEncodingError, got %T", err)
		assert.Equal(t, CBOREncoding, invalid.Expected)
		assert.Equal(t, JSONEncoding, invalid.Actual)
		assert.Equal(t, codecPayload{}, decoded)
	})
}

func TestCodec(t *testing.T) {
	t.Run("discriminator strings match Rust (CBOR-S3.R3)", discriminatorStringsMatchRust)
	t.Run("round-trips through JSON and CBOR (CBOR-S1.R1, CBOR-S1.R2, CBOR-S3.R2)", roundTripsThroughJSONAndCBOR)
	t.Run("JSON encoder preserves pre-feature bytes (CBOR-S3.R2)", jsonEncoderPreservesPreFeatureBytes)
	t.Run("CBOR encode failure returns error, no fallback (CBOR-S1.R3)", cborEncodeFailureReturnsErrorNoFallback)
	t.Run("Decoders select by encoding (CBOR-S2.R1)", decodersSelectByEncoding)
	t.Run("Decoders handle mixed-encoding stream (CBOR-S2.R2)", decodersHandleMixedEncodingStream)
	t.Run("Decoders reject unknown encoding (CBOR-S2.R3)", decodersRejectUnknownEncoding)
	t.Run("direct decoder rejects mismatched encoding", directDecoderRejectsMismatchedEncoding)
}

// CBOR-S3.R1 - the default encoder (used by MarshalToData) emits
// application/json.
func defaultEncoderEmitsJSON(t *testing.T) {
	encoded, err := MarshalToData(codecPayload{Name: "widget", Count: 7})
	require.NoError(t, err)
	assert.Equal(t, JSONEncoding, encoded.Encoding)
}

// CBOR-S2.R1, CBOR-S2.R3 - UnmarshalFromData dispatches by encoding and rejects
// unknown encodings with the typed error.
func unmarshalFromDataDispatchesAndRejects(t *testing.T) {
	original := codecPayload{Name: "widget", Count: 7}

	t.Run("decodes json default", func(t *testing.T) {
		encoded, err := MarshalToData(original)
		require.NoError(t, err)
		var decoded codecPayload
		require.NoError(t, UnmarshalFromData(encoded, &decoded))
		assert.Equal(t, original, decoded)
	})

	t.Run("decodes cbor", func(t *testing.T) {
		encoded, err := MakeCBOREncoder().Encode(original)
		require.NoError(t, err)
		var decoded codecPayload
		require.NoError(t, UnmarshalFromData(encoded, &decoded))
		assert.Equal(t, original, decoded)
	})

	t.Run("rejects unknown encoding", func(t *testing.T) {
		var decoded codecPayload
		err := UnmarshalFromData(Data{Encoding: "application/x-unknown", Data: []byte("{}")}, &decoded)
		require.Error(t, err)
		var unknown *UnknownEncodingError
		assert.True(t, errors.As(err, &unknown), "expected *UnknownEncodingError, got %T", err)
	})
}

func TestDataMarshaller(t *testing.T) {
	t.Run("default encoder emits application/json (CBOR-S3.R1)", defaultEncoderEmitsJSON)
	t.Run("UnmarshalFromData dispatches and rejects (CBOR-S2.R1, CBOR-S2.R3)", unmarshalFromDataDispatchesAndRejects)
}
