package we

import (
	"fmt"
)

type InvalidEncodingError struct {
	Expected string
	Actual   string
}

func (e *InvalidEncodingError) Error() string {
	if e.Expected == "" {
		return fmt.Sprintf("unknown encoding %s", e.Actual)
	}
	return fmt.Sprintf("expected encoding %s, got %s", e.Expected, e.Actual)
}

func InvalidEncoding(expected string, actual string) error {
	return &InvalidEncodingError{
		Expected: expected,
		Actual:   actual,
	}
}

// UnknownEncoding reports that no decoder is registered for the given encoding.
// It is the typed unknown-encoding error used by Decoders dispatch (CBOR-S2.R3,
// CBOR-S4.R2); Expected is empty because no single encoding was required.
func UnknownEncoding(actual string) error {
	return &InvalidEncodingError{
		Expected: "",
		Actual:   actual,
	}
}

// defaultEncoder is the framework's default event encoder. JSON is the default
// so existing stores and Go/Rust interop are unaffected (CBOR-S3.R1, ADR-0001).
var defaultEncoder Encoder = MakeJSONEncoder()

// defaultDecoders dispatches by the envelope's encoding across the built-in
// JSON and CBOR decoders (CBOR-S2).
var defaultDecoders = MakeDecoders(MakeJSONDecoder(), MakeCBORDecoder())

// MarshalToData encodes event with the default (JSON) encoder (CBOR-S3.R1).
func MarshalToData(event any) (Data, error) {
	return defaultEncoder.Encode(event)
}

// UnmarshalFromData decodes a Data envelope by selecting a decoder for its
// declared encoding (CBOR-S2.R1). An unknown encoding yields a typed
// unknown-encoding error with no default decode (CBOR-S2.R3).
func UnmarshalFromData(data Data, value any) error {
	return defaultDecoders.Decode(data, value)
}
