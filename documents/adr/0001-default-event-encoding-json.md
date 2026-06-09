# ADR-0001 — JSON is the default event encoding

- **Status:** Accepted
- **Relates to:** [features/01-cbor-codec.md](../features/01-cbor-codec.md) · [ADR-0002](0002-cbor-library-fxamacker.md)

## Context

`wee-events-go` is introducing a pluggable codec layer so event payloads can be encoded
as either `application/json` or `application/cbor` (see feature 01). The `Data` envelope
already carries an `encoding` discriminator, so stores persist whichever encoding was
used and never interpret the bytes.

Two constraints bear on which encoding is the default writer:

- The DynamoDB, JetStream, and Kurrent backends hold payloads that are read and written
  by both the Go and the `wee-events.rs` families. The Rust core's default writer is
  JSON (`EventData::JSON_ENCODING`).
- The existing Go store tests and any deployed data are JSON. Changing the default would
  alter the bytes written for callers who never opted into a new encoding.

## Decision

The framework will use `application/json` as the default event encoder. CBOR is opt-in
per writer; selecting it is an explicit choice by the application, never implicit.

## Consequences

- Cross-language interop on shared stores is preserved without migration: a Go service
  and a Rust service reading the same stream both default to JSON.
- Existing JSON-backed deployments and tests are byte-compatible; the codec refactor is
  additive.
- Applications that want CBOR's compactness must construct a CBOR encoder explicitly,
  and must ensure every reader of that stream has a CBOR decoder registered.
- The exact discriminator strings `application/json` and `application/cbor` are part of
  the on-disk/on-wire contract and must match the Rust constants byte-for-byte.

JSON-as-default is the correct choice because it is the only option that adds CBOR
without changing the bytes any existing caller already writes: the default path is
byte-identical to today and to the Rust sibling, so parity and interop hold with zero
migration, while CBOR is fully available to anyone who opts in.

## Alternatives considered

- **Default to CBOR for compactness.** Rejected: it would change the bytes written for
  existing callers and break interop with JSON-only readers, including the current Rust
  default. The compactness win is still available via explicit opt-in, so defaulting to
  CBOR trades a breaking change for a benefit already reachable without one.
- **Keep a single fixed encoding (JSON-only, no codec layer).** Rejected: it forecloses
  the compactness and (de)serialisation-cost benefits CBOR offers high-volume aggregates,
  which is the entire point of feature 01. The pluggable layer costs little and the
  default still pins JSON, so a fixed encoding gives up the upside for no real saving.
- **Make the default encoding configurable per store or deployment.** Rejected: a
  configurable default reintroduces the interop hazard this ADR exists to remove — two
  deployments could silently disagree on the bytes for the same stream — and adds a
  global knob where a predictable constant plus per-writer opt-in already covers every
  need.
