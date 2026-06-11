package we

import (
	"encoding/json"

	"github.com/fxamacker/cbor/v2"
)

// Encoding discriminator strings. These are part of the on-wire/on-disk
// contract and MUST match the wee-events.rs sibling byte-for-byte (CBOR-S3.R3).
const (
	JSONEncoding = "application/json"
	CBOREncoding = "application/cbor"
)

// Encoder serializes a value into a Data envelope tagged with its encoding.
// An Encoder never falls back to another encoding on failure (CBOR-S1.R3).
type Encoder interface {
	// Encoding reports the discriminator this encoder produces.
	Encoding() string
	// Encode serializes value and returns a Data envelope tagged with Encoding.
	Encode(value any) (Data, error)
}

// Decoder deserializes the bytes of a Data envelope into a value.
// A Decoder never falls back to another encoding on failure (CBOR-S2.R3).
type Decoder interface {
	// Encoding reports the discriminator this decoder consumes.
	Encoding() string
	// Decode deserializes data.Data into value.
	Decode(data Data, value any) error
}

// JSONEncoder encodes payloads as application/json. There is no implicit
// framework default (ADR-0007); JSON is the recommended choice for
// cross-family interop and produces bytes identical to the pre-codec path
// (CBOR-S3.R2).
type JSONEncoder struct{}

// MakeJSONEncoder returns a JSONEncoder that emits application/json payloads.
func MakeJSONEncoder() JSONEncoder {
	return JSONEncoder{}
}

func (JSONEncoder) Encoding() string {
	return JSONEncoding
}

func (JSONEncoder) Encode(value any) (Data, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return Data{}, err
	}

	return Data{
		Encoding: JSONEncoding,
		Data:     encoded,
	}, nil
}

// JSONDecoder decodes application/json payloads.
type JSONDecoder struct{}

// MakeJSONDecoder returns a JSONDecoder that consumes application/json payloads.
func MakeJSONDecoder() JSONDecoder {
	return JSONDecoder{}
}

func (JSONDecoder) Encoding() string {
	return JSONEncoding
}

func (JSONDecoder) Decode(data Data, value any) error {
	if data.Encoding != JSONEncoding {
		return InvalidEncoding(JSONEncoding, data.Encoding)
	}
	return json.Unmarshal(data.Data, value)
}

// CBOREncoder encodes payloads as application/cbor (CBOR-S1.R2) using
// fxamacker/cbor/v2 (ADR-0002).
type CBOREncoder struct{}

// MakeCBOREncoder returns a CBOREncoder that emits application/cbor payloads.
func MakeCBOREncoder() CBOREncoder {
	return CBOREncoder{}
}

func (CBOREncoder) Encoding() string {
	return CBOREncoding
}

func (CBOREncoder) Encode(value any) (Data, error) {
	encoded, err := cbor.Marshal(value)
	if err != nil {
		return Data{}, err
	}

	return Data{
		Encoding: CBOREncoding,
		Data:     encoded,
	}, nil
}

// hardenedCBORDecMode backs HardenedCBORUnmarshal — the policy is documented
// there. CBORDecoder shares it so the codec decode path and the exported entry
// can never drift apart.
var hardenedCBORDecMode = mustCBORDecMode(cbor.DecOptions{
	DupMapKey:       cbor.DupMapKeyEnforcedAPF,
	IndefLength:     cbor.IndefLengthForbidden,
	MaxNestedLevels: 16,
})

func mustCBORDecMode(opts cbor.DecOptions) cbor.DecMode {
	mode, err := opts.DecMode()
	if err != nil {
		panic(err)
	}
	return mode
}

// HardenedCBORUnmarshal is THE hardened CBOR decode entry for untrusted or
// at-rest bytes: command bodies arriving off the wire and store envelopes read
// back from disk. It is the framework's single decode-side security policy —
// every CBOR decode outside this package goes through it, never through the
// unhardened package-level cbor.Unmarshal. Three protections apply:
//
//   - duplicate map keys are rejected (DupMapKeyEnforcedAPF), so a crafted
//     payload cannot smuggle a second value past validation of the first;
//   - indefinite-length items are forbidden (IndefLengthForbidden) — the
//     encoders only ever emit definite-length CBOR, so an indefinite item is
//     at best foreign and at worst a streaming-abuse vector;
//   - nesting depth is capped at 16 (MaxNestedLevels) to bound stack and
//     resource use against deeply nested payloads.
//
// Hardening is decode-side only: encoding trusted in-process values needs no
// defence, so the encode path stays on the package-level cbor.Marshal.
func HardenedCBORUnmarshal(data []byte, v any) error {
	return hardenedCBORDecMode.Unmarshal(data, v)
}

// CBORDecoder decodes application/cbor payloads.
type CBORDecoder struct {
	decMode cbor.DecMode
}

// MakeCBORDecoder returns a CBORDecoder that consumes application/cbor payloads
// using a hardened decode mode suitable for untrusted bytes.
func MakeCBORDecoder() CBORDecoder {
	return CBORDecoder{decMode: hardenedCBORDecMode}
}

func (CBORDecoder) Encoding() string {
	return CBOREncoding
}

func (d CBORDecoder) Decode(data Data, value any) error {
	if data.Encoding != CBOREncoding {
		return InvalidEncoding(CBOREncoding, data.Encoding)
	}
	return d.decMode.Unmarshal(data.Data, value)
}

// Decoders selects a Decoder by a Data envelope's encoding (CBOR-S2.R1) and
// decodes each envelope with the matching decoder regardless of stream order
// (CBOR-S2.R2). An envelope whose encoding names no registered decoder yields a
// typed unknown-encoding error with no default decode (CBOR-S2.R3).
type Decoders struct {
	decoders map[string]Decoder
}

// MakeDecoders builds a Decoders keyed by each decoder's Encoding(). A later
// decoder for the same encoding replaces an earlier one.
func MakeDecoders(decoders ...Decoder) Decoders {
	registry := make(map[string]Decoder, len(decoders))
	for _, decoder := range decoders {
		registry[decoder.Encoding()] = decoder
	}

	return Decoders{decoders: registry}
}

// Decode selects the registered decoder for data.Encoding and decodes into
// value. If no decoder is registered for the encoding it returns a typed
// unknown-encoding error and does not attempt any default decode (CBOR-S2.R3).
func (d Decoders) Decode(data Data, value any) error {
	decoder, ok := d.decoders[data.Encoding]
	if !ok {
		return UnknownEncoding(data.Encoding)
	}

	return decoder.Decode(data, value)
}
