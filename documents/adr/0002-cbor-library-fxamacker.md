# ADR-0002 — Use `fxamacker/cbor/v2` for CBOR

- **Status:** Accepted
- **Relates to:** [features/01-cbor-codec.md](../features/01-cbor-codec.md) · [ADR-0001](0001-default-event-encoding-json.md)

## Context

Feature 01 adds a CBOR encoder/decoder alongside JSON. Go has several CBOR libraries;
the choice matters because event encoding is on the hot path and the bytes become part
of the stored contract. The `wee-events.rs` sibling uses `ciborium`, so the Go choice
should produce interoperable, well-formed CBOR.

Requirements on the library:

- `encoding/json`-parity ergonomics (struct tags, marshal/unmarshal) so adopting it is a
  drop-in alongside the existing JSON path.
- Deterministic/canonical encoding modes, so the same value encodes to the same bytes
  (useful for tests and for any future content-addressing).
- Maintained, widely used, and buildable under Go 1.26.

## Decision

The framework will use `github.com/fxamacker/cbor/v2` for CBOR encoding and decoding.

## Consequences

- The CBOR codec mirrors the JSON codec's shape, keeping the pluggable-codec layer
  uniform.
- Deterministic encoding modes are available when needed.
- A new direct dependency is added; it must be confirmed to build under Go 1.26 and kept
  current via `just update-deps`.
- Encode/decode failures surface as typed codec errors and must not silently fall back
  to another encoding (see feature 01's unwanted-behaviour requirements).

`fxamacker/cbor/v2` is the correct choice because it is interop-neutral and the lowest-
friction fit. CBOR is a standardised wire format (RFC 8949), so any conformant library's
output is readable by the Rust sibling's `ciborium` and vice versa — library choice
carries no cross-language interop risk, unlike a bespoke format would. With interop off
the table, the deciding factors are `encoding/json`-parity ergonomics, deterministic
encoding modes, and maintenance health, on all of which `fxamacker/cbor/v2` is the
strongest maintained option.

## Alternatives considered

- **`github.com/ugorji/go/codec`.** Rejected: it targets many formats (CBOR, MessagePack,
  Binc, JSON) behind one heavier, code-generation-oriented API; for a single-format CBOR
  need it adds surface and ceremony without a corresponding benefit over a focused library.
- **Match the Rust sibling's library (`ciborium`).** Not applicable as stated — `ciborium`
  is Rust-only — and the underlying motive (interoperable bytes) is already satisfied by
  any RFC 8949-conformant Go library, so there is no reason to constrain the Go choice to
  mirror the Rust one.
- **A standard-library option.** Rejected: Go has no standard CBOR codec, so this is not
  available.
