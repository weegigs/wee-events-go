package we

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// jsonSpelledData is the JSON-medium shape of the presentation contract:
// the raw value of "data" is held unparsed so payload bytes tagged
// JSONEncoding pass through verbatim in both directions.
type jsonSpelledData struct {
	Encoding string          `json:"encoding"`
	Data     json.RawMessage `json:"data"`
}

// MarshalJSON defines the canonical JSON-medium spelling of the presentation
// contract (ADR-0011 decision 5) — the spelling every JSON edge shares, not a
// wire policy of any one edge. It is total over payload encodings: payload
// bytes tagged JSONEncoding are byte-spliced into the output as the raw JSON
// value of "data" — never re-encoded, so interior whitespace and unescaped
// HTML characters from foreign producers survive byte-for-byte. Two refusals
// guard that verbatim promise: bytes that are not valid JSON are corrupt, and
// bytes with leading or trailing whitespace have no verbatim spelling — a
// JSON reader capturing the "data" value cannot see whitespace surrounding
// it. Bytes under any other tag are spelled as a base64 string, the JSON
// medium's only total spelling of binary. The zero value Data{} spells as
// {"encoding":"","data":null} and round-trips to itself: the empty tag takes
// the binary branch, where nil bytes spell null. The verbatim promise covers
// this method's own output: an enclosing encoding/json document re-encodes
// nested marshaller output (compaction, HTML escaping), so byte-stability
// through a larger json.Marshal holds only for canonical encoder-produced
// payload bytes (ADR-0011 decision 5).
func (d Data) MarshalJSON() ([]byte, error) {
	if d.Encoding == JSONEncoding {
		if !json.Valid(d.Data) {
			return nil, fmt.Errorf("we: payload tagged %s is not valid JSON and has no JSON spelling", JSONEncoding)
		}
		if len(bytes.TrimSpace(d.Data)) != len(d.Data) {
			return nil, fmt.Errorf("we: payload tagged %s has leading or trailing whitespace and no verbatim JSON spelling", JSONEncoding)
		}

		spelled := make([]byte, 0, len(`{"encoding":"`)+len(JSONEncoding)+len(`","data":`)+len(d.Data)+len(`}`))
		spelled = append(spelled, `{"encoding":"`...)
		spelled = append(spelled, JSONEncoding...)
		spelled = append(spelled, `","data":`...)
		spelled = append(spelled, d.Data...)
		spelled = append(spelled, '}')
		return spelled, nil
	}

	encoded, err := json.Marshal(d.Data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(jsonSpelledData{Encoding: d.Encoding, Data: json.RawMessage(encoded)})
}

// UnmarshalJSON parses the canonical JSON-medium spelling. Under JSONEncoding
// the raw value of "data" is captured verbatim, whatever it is — "data":null
// captures the four bytes `null`, a valid JSON value that re-spells
// identically. Under any other tag the base64 string spelling is decoded,
// with null yielding nil payload bytes. An absent "data" yields nil payload
// bytes under every tag.
func (d *Data) UnmarshalJSON(b []byte) error {
	var spelled jsonSpelledData
	if err := json.Unmarshal(b, &spelled); err != nil {
		return err
	}

	d.Encoding = spelled.Encoding
	if len(spelled.Data) == 0 {
		d.Data = nil
		return nil
	}
	if spelled.Encoding == JSONEncoding {
		d.Data = []byte(spelled.Data)
		return nil
	}

	var payload []byte
	if err := json.Unmarshal(spelled.Data, &payload); err != nil {
		return err
	}
	d.Data = payload
	return nil
}
