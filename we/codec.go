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

// JSONEncoder encodes payloads as application/json. It is the framework
// default (CBOR-S3.R1) and produces bytes identical to the pre-codec path
// (CBOR-S3.R2).
type JSONEncoder struct{}

func MakeJSONEncoder() JSONEncoder {
	return JSONEncoder{}
}

func (JSONEncoder) Encoding() string {
	return JSONEncoding
}

func (JSONEncoder) Encode(value any) (Data, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return Data{}, err
	}

	return Data{
		Encoding: JSONEncoding,
		Data:     bytes,
	}, nil
}

// JSONDecoder decodes application/json payloads.
type JSONDecoder struct{}

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

func MakeCBOREncoder() CBOREncoder {
	return CBOREncoder{}
}

func (CBOREncoder) Encoding() string {
	return CBOREncoding
}

func (CBOREncoder) Encode(value any) (Data, error) {
	bytes, err := cbor.Marshal(value)
	if err != nil {
		return Data{}, err
	}

	return Data{
		Encoding: CBOREncoding,
		Data:     bytes,
	}, nil
}

// CBORDecoder decodes application/cbor payloads.
type CBORDecoder struct{}

func MakeCBORDecoder() CBORDecoder {
	return CBORDecoder{}
}

func (CBORDecoder) Encoding() string {
	return CBOREncoding
}

func (CBORDecoder) Decode(data Data, value any) error {
	if data.Encoding != CBOREncoding {
		return InvalidEncoding(CBOREncoding, data.Encoding)
	}
	return cbor.Unmarshal(data.Data, value)
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
