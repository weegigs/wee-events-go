# Feature 01 — Pluggable Codec + CBOR Support

- **Status:** Planned · **Size:** M · **Area:** core (`we/`)
- **Coordinates with:** [Feature 05](05-rejection-error-taxonomy.md) (shared `we/command.go`)
- **Prefix:** `CBOR`

## Summary

Make event and command-payload serialization codec-agnostic at the boundary, and add
CBOR alongside JSON. Today the Go core hardcodes `application/json` in `MarshalToData`;
the `Data` envelope already carries an `encoding` discriminator, so the wire format is
encoding-aware while the marshaller is not. After this feature, an application can record
and read events as either JSON or CBOR, a stream may mix encodings, and JSON remains the
default so existing stores and Go/Rust interop are unaffected.

## Decisions

- [ADR-0001](../adr/0001-default-event-encoding-json.md) — JSON is the default event
  encoding; CBOR is opt-in.
- [ADR-0002](../adr/0002-cbor-library-fxamacker.md) — CBOR is provided by
  `github.com/fxamacker/cbor/v2`.

## User stories

### CBOR-S1 — Record events as CBOR

*As an application developer, I want to record event payloads using CBOR, so that
high-volume aggregates store compact bytes that are faster to (de)serialize.*

- **CBOR-S1.R1** (ubiquitous) — The framework shall support encoding event payloads as
  either `application/json` or `application/cbor`.
- **CBOR-S1.R2** (event-driven) — When an event is recorded through a CBOR encoder, the
  framework shall produce a `Data` envelope whose `encoding` is `application/cbor` and
  whose bytes are the CBOR serialization of the payload. *(See ADR-0002.)*
- **CBOR-S1.R3** (unwanted) — If a value cannot be CBOR-encoded, then the framework shall
  return an encode error and shall not fall back to another encoding. *(No-workarounds
  policy.)*

### CBOR-S2 — Decode by declared encoding

*As an application developer, I want events to decode according to the encoding they were
written with, so that a stream containing both JSON and CBOR events rebuilds correctly.*

- **CBOR-S2.R1** (event-driven) — When decoding a `Data` envelope, the framework shall
  select a decoder by the envelope's `encoding` field.
- **CBOR-S2.R2** (state-driven) — While both the JSON and CBOR decoders are registered,
  the framework shall decode each event using the decoder whose encoding matches the
  envelope, regardless of order in the stream.
- **CBOR-S2.R3** (unwanted) — If a `Data` envelope's `encoding` names no registered
  decoder, then the framework shall return a typed unknown-encoding error and shall not
  attempt a default decode.

### CBOR-S3 — JSON default and backward compatibility

*As a maintainer of an existing JSON-backed deployment, I want JSON to remain the default
and wire-compatible, so that existing stores and cross-language interop are unaffected.*

- **CBOR-S3.R1** (ubiquitous) — The framework shall use `application/json` as the default
  event encoder. *(See ADR-0001.)*
- **CBOR-S3.R2** (event-driven) — When a payload stored as `application/json` is decoded,
  the framework shall produce a value byte-for-byte equivalent to the pre-feature
  behaviour.
- **CBOR-S3.R3** (ubiquitous) — The framework shall preserve the exact discriminator
  strings `application/json` and `application/cbor` as used by the `wee-events.rs` sibling.

### CBOR-S4 — Encoding-aware remote commands

*As an API client, I want to submit commands with a declared payload encoding, so that
command payloads are not restricted to JSON.*

- **CBOR-S4.R1** (event-driven) — When a `RemoteCommand` declares its payload encoding,
  the framework shall decode the payload using the registered decoder for that encoding.
- **CBOR-S4.R2** (unwanted) — If a `RemoteCommand` declares an unsupported encoding, then
  the framework shall reject the command with a typed unknown-encoding error.

## Implementation notes

### Current Go state

`we/data-marshaller.go` hardcodes JSON in both directions (`MarshalToData` /
`UnmarshalFromData`, the latter rejecting any non-`application/json` encoding). `Data`
already holds `{Encoding string, Data []byte}`. `RemoteCommand` in `we/command.go`
decodes its payload the same JSON-only way.

### Rust reference (port origin)

`crates/wee-events/src/codec.rs`: `EventEncoder` / `EventDecoder` traits;
`JsonEncoder`/`JsonDecoder` (`application/json`); `CborEncoder`/`CborDecoder`
(`application/cbor`, via `ciborium`); `EventDecoders<List>` — the polymorphic decoder
that selects by `data.encoding`; and the `EncodeError` / `DecodeError` / `CodecError`
taxonomy (the unified `CodecError` is consumed by [Feature 05](05-rejection-error-taxonomy.md)).

### Go target

- New `we/codec.go`: `Encoder` and `Decoder` interfaces (each exposing `Encoding()`);
  `JSONEncoder`/`JSONDecoder`, `CBOREncoder`/`CBORDecoder`; and a `Decoders` aggregate
  keyed by encoding that dispatches per `Data.Encoding` (satisfies CBOR-S2).
- Refactor `we/data-marshaller.go`: `MarshalToData` keeps its JSON default behaviour but
  is reimplemented over `JSONEncoder` (one code path); `UnmarshalFromData` dispatches via
  `Decoders`, preserving `InvalidEncodingError` for the unknown-encoding case.
- Extend `we/command.go`: `RemoteCommand` payload decode dispatches on declared encoding
  (satisfies CBOR-S4). **This file is also edited by Feature 05 — sequence 01 → 05 or
  co-own.**
- Library: `github.com/fxamacker/cbor/v2` (ADR-0002); add via `just tidy`.

## Verification

| Requirement | Test |
|---|---|
| CBOR-S1.R1, CBOR-S1.R2, CBOR-S3.R2 | Round-trip a representative event through JSON and CBOR codecs; assert decoded value equals original and `Data.Encoding` is correct for each. |
| CBOR-S1.R3 | Encode a non-CBOR-encodable value; assert a typed encode error, no fallback. |
| CBOR-S2.R1, CBOR-S2.R2 | Encode the same value as CBOR; hand it to a `Decoders` with both JSON and CBOR registered; assert correct decode by encoding selection, including a mixed-encoding stream. |
| CBOR-S2.R3, CBOR-S4.R2 | Decode a `Data{Encoding: "application/x-unknown"}`; assert typed unknown-encoding error, no panic, no default decode. |
| CBOR-S3.R1, CBOR-S3.R3 | Assert the default encoder emits `application/json`; assert discriminator strings match the Rust constants. |
| CBOR-S3.R2 (regression) | Existing `ds`/`jetstream`/`kurrent` store tests remain green with no changes. |
| CBOR-S4.R1 | Submit a `RemoteCommand` with a CBOR payload; assert it decodes and dispatches. |

Verification is by running these tests (`just test`), not by assertion.
