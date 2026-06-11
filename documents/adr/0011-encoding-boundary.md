# ADR-0011 — Encoding boundary: presentation contract, verbatim round-trip, store-owned storage format

- **Status:** Accepted
- **Relates to:** [features/08-explicit-event-encoding.md](../features/08-explicit-event-encoding.md) · [ADR-0010](0010-identity-grammar.md) (the stores-adapt principle this decision twins)

## Context

`we.Data` carried its payload as `json.RawMessage`, coupling the event
envelope to JSON payloads. The ds and jetstream stores serialise their
envelopes as JSON documents — jetstream through a default `JSONMarshaller`,
ds by `json.Marshal`-ling recorded events into a string item attribute in
`makeChangeSet` — so a CBOR payload failed loudly at publish on both
backends. Feature 08 recorded that limit as the BLOB-scoping caveat on
ENCODING-S3.R2: end-to-end CBOR was scoped to BLOB-backed stores only. The
first proposed fix — retype the field to `[]byte` and let `encoding/json`
spell the bytes as base64 — was rejected in owner review: storage encoding
must be optimal for the store and distinct from payload encoding, and a text
envelope that base64s binary inside a binary-capable medium is a workaround.
It patches the symptom while leaving the cause: a store layout choice ("the
envelope is a JSON document") reaching up to constrain what payloads the
framework can carry.

## Decision

1. **Layer model.** Four layers, each with one owner: the *application
   format* (typed events, per language), the *wire format* (API edges:
   wehttp, werestate), the *presentation contract*
   (`we.Data{encoding, []byte}` — what `Publish` hands a store and what
   `Load` hands back; the `encoding` tag instructs the reader how to decode
   the bytes presented), and the *storage format* (store-owned, per
   backend).
2. **Verbatim rule.** A store re-presents on read exactly the payload bytes
   it was handed on publish, with the original tag. Transcoding is
   forbidden: it is lossy at the edges — CBOR byte strings, integer keys,
   and tags have no JSON image — and verbatim bytes are what make hashing,
   signing, and audit of events possible. Storage freedom is layout
   freedom, not content freedom: a store never rejects, transforms, or
   constrains a payload encoding. This is the encoding-domain twin of the
   identity spec's stores-adapt rule (ADR-0010).
3. **`we.Data.Data` is retyped `json.RawMessage` → `[]byte`** — the honest
   type of an opaque tagged byte presentation.
4. **Store layouts move below the boundary; storage encoding is
   store-chosen and optimal for the medium.** The jetstream envelope
   default becomes CBOR (`fxamacker/cbor/v2`, ADR-0002) and
   `JSONMarshaller` is deleted — a text envelope is suboptimal in a
   binary-capable medium and would base64 payload bytes; the `Marshaller`
   seam remains. The ds store persists recorded events as CBOR bytes in a
   native DynamoDB binary (`B`) attribute instead of a JSON string
   attribute.
5. **Wire spellings are total and edge-negotiated.** Every medium's
   spelling of `we.Data` is total over payload encodings — a wire that
   rejects a valid envelope is a partial function, the encoding twin of a
   store constraining identities. The JSON wire embeds JSON payload bytes
   as raw JSON — never base64 — preserving the existing wire shape, and
   spells binary payload bytes as base64, the JSON medium's only total
   option; a CBOR wire carries every payload encoding as native bytes.
   Byte-verbatim embedding through an enclosing JSON document holds for
   canonical encoder-produced payload bytes (Go's `encoding/json`
   normalises nested marshaller output); parsing captures bytes verbatim
   in every case, binary mediums are verbatim unconditionally, and the
   verbatim rule of decision 2 binds the store boundary, which does not
   traverse a JSON medium. Totality is over encodings, not corrupt
   content: bytes tagged `application/json` that are not a JSON value, or
   that carry surrounding whitespace (invisible to a JSON value capture),
   have no JSON spelling and refuse to marshal. `we.Data`
   defines its canonical JSON spelling once via `MarshalJSON`/
   `UnmarshalJSON`; CBOR is the encouraged wire (wehttp accepts
   `application/cbor` request bodies and serves CBOR responses negotiated
   via `Accept`; JSON remains the default).
6. **Per-publish and per-constructor encoder choice is retained.**
   `WithEncoder` survives unchanged: under the presentation contract the
   publisher legitimately chooses the presentation, and consumers dispatch
   on the tag per event.
7. **Previously stored ds/jetstream development events are orphaned, not
   migrated** (owner-confirmed; standing precedent per ADR-0010 — the port
   is unreleased).

## Consequences

- ENCODING-S3.R2 becomes unscoped: every backend carries every encoding,
  verified by a cross-backend conformance-suite round-trip scenario
  asserting verbatim bytes and the original tag.
- Base64 vanishes from rest as a consequence of storage-encoding
  optimality; it survives only as the JSON wire's documented spelling of
  binary payload bytes, and the encouraged CBOR wire avoids it entirely.
- ds and jetstream envelopes are no longer human-readable at rest;
  inspection becomes a tooling concern, not a layout constraint.
- Development events written under the old ds/jetstream layouts are
  orphaned.
- Wire formats are unaffected: HTTP and Restate edges sit above the
  application format and render their own representations regardless of
  any store's layout.

## Alternatives considered

- **Base64-in-JSON storage envelopes** (retype to `[]byte`, keep
  JSON-document envelopes, let `encoding/json` base64 the bytes). Rejected:
  a text envelope is the wrong storage encoding for binary-capable mediums,
  and it patches the symptom while leaving store layout coupled to the
  presentation contract.
- **Store-declared supported encodings.** Rejected: capability negotiation
  breaks store substitutability, conformance uniformity, and
  cross-store/cross-language portability, and surfaces as late runtime
  failures instead of a uniform contract.
- **Semantic-equivalence round-trip (transcoding stores).** "Present with
  this encoding" is satisfiable in principle by a store that persists CBOR
  and presents JSON. Rejected: generic transcoding is lossy at the edges
  and breaks byte-level hashing and signing.
- **Embed JSON payloads verbatim, base64 only non-JSON.** Rejected: the
  envelope shape would vary by payload encoding — a tagged union every
  reader must dispatch on.
