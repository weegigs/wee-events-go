# Encoding Boundary Design — Presentation Contract vs Storage Format

**Status:** Draft for owner approval · **Replaces:** Phase 4 (Tasks 13–16) of
[2026-06-10-identity-grammar-and-followups.md](../plans/2026-06-10-identity-grammar-and-followups.md)
**Date:** 2026-06-10

## Decision

`we.Data` is a **presentation contract**, not a storage description. The
`encoding` tag instructs the *reader*: "decode the bytes presented here with
this encoding." Storage format — how a backend lays the envelope and payload
bytes down at rest — is entirely store-owned. Concretely:

1. `we.Data.Data` is retyped `json.RawMessage` → `[]byte`: the honest type of
   an opaque tagged byte presentation.
2. **Verbatim rule:** a store re-presents on read exactly the bytes it was
   handed on publish, with the original tag. Stores never transcode,
   transform, or constrain a payload encoding. (Layout freedom, not content
   freedom.)
3. **Storage encoding is store-chosen, optimal for the medium, and distinct
   from payload encoding.** Both current JSON-document envelopes sit in
   binary-capable mediums, so both move to binary (base64 disappears from
   rest as a consequence of optimality, not as a rule):
   - `stores/jetstream`: the default envelope marshaller becomes CBOR
     (fxamacker/cbor/v2, ADR-0002). `JSONMarshaller` is deleted — a text
     envelope is suboptimal in a binary-capable medium and would base64
     payload bytes. The `Marshaller` seam remains.
   - `stores/ds`: `ChangeSet.Events` is retyped `string` → `[]byte` holding
     CBOR-marshalled recorded events; `attributevalue` persists `[]byte` as a
     native DynamoDB binary (`B`) attribute. The other item attributes
     (keys, revision, timestamp) stay as they are — readable.
4. **Wire spellings are total and edge-negotiated.** Every medium's
   spelling of `we.Data` is total over payload encodings — a wire that
   rejects a valid envelope is a partial function, the same sin as a store
   declaring supported encodings. The JSON wire embeds JSON payload bytes
   as raw JSON — never base64 — and spells binary payload bytes as base64,
   the JSON medium's only total option. A CBOR wire carries every payload
   encoding as native bytes. Precision on "verbatim" in the JSON medium:
   the canonical spelling emits payload bytes verbatim and parsing captures
   them verbatim, but Go's `encoding/json` normalises nested marshaller
   output (compaction, HTML escaping), so byte-stability through an
   enclosing JSON document holds for canonical encoder-produced bytes;
   non-canonical foreign bytes are byte-stable only in binary mediums. The
   verbatim *rule* (decision 2) binds the store boundary, which after this
   phase never traverses a JSON medium; wire-side normalisation of command
   payload formatting is immaterial because edges decode semantically and
   stored bytes come from the publisher's encoder. Totality is over
   encodings, not corrupt content: bytes tagged `application/json` that
   are not a JSON value, or that carry surrounding whitespace (invisible
   to a JSON value capture), have no JSON spelling and refuse to marshal.
   `we.Data` gains canonical `MarshalJSON`/`UnmarshalJSON` defining the
   JSON spelling once, for every edge. CBOR is the encouraged wire, and wehttp
   speaks it in both directions in this phase: `application/cbor` request
   bodies, and CBOR responses negotiated via the `Accept` header (JSON
   remains the default).

   | | JSON payload bytes | binary payload bytes (CBOR…) |
   |---|---|---|
   | JSON wire | embedded raw JSON | base64 |
   | CBOR wire | native byte string | native byte string |

5. Previously stored ds/jetstream development events are **orphaned, not
   migrated** (standing precedent, ADR-0010 restates it; the port is
   unreleased).
6. ADR-0011 records the boundary; feature 08's BLOB-scoping caveats are
   removed and ENCODING-S3.R2 becomes unscoped: every backend carries every
   encoding, verified by a cross-backend CBOR round-trip conformance
   scenario.

## The layer model

| Layer | Owner | Example |
|---|---|---|
| Application format | each language | `Counter{Value: 7}` (Go/Rust struct) |
| Wire format | the API edge | wehttp resource JSON; werestate payloads |
| Presentation contract | `we.Data{encoding, []byte}` | what Publish hands a store; what Load hands back |
| Storage format | each store | NATS message body layout; DynamoDB item shape; Kurrent record |

```
WRITE                                          READ
Counter{Value: 7}                              Counter{Value: 7}
  │ encoder (constructor default                 ▲ decoder dispatched on tag
  ▼  or WithEncoder)                             │
Data{Encoding:"cbor", Data:[]byte}             Data{Encoding:"cbor", Data:[]byte}
══╪══ store boundary — bytes re-presented VERBATIM with original tag ══╪══
  ▼ store-owned layout                           ▲ payload bytes untouched
  jetstream: CBOR envelope → msg body            envelope deserialised
  ds:        recorded events as CBOR → B attr
  kurrent/sqlite/memory: already byte-native
```

The wire format does not appear in the flow: HTTP/Restate edges sit above the
application format and render their own representations regardless of any
store's layout.

## Why verbatim, not semantic equivalence

"Present with this encoding" is satisfiable in principle by a transcoding
store (persist CBOR internally, present JSON on read). Rejected:

- Generic transcoding is lossy at the edges — CBOR byte strings, integer
  keys, and tags have no JSON image. Permitting it invites silent corruption.
- Verbatim payload bytes are what make hashing, signing, and audit of events
  possible.
- Every current backend already behaves verbatim; the rule costs nothing and
  the conformance scenario pins it.

## What this dissolves from the original Phase 4

The original Task 13 kept JSON-document envelopes and relied on
`encoding/json` spelling `[]byte` as base64. That patched the symptom (a
`RawMessage` field that cannot carry CBOR) while leaving the cause: ds and
jetstream let a layout choice — "the envelope is a JSON document" — reach
up and constrain the presentation contract. Owner correction: storage
encoding must be optimal for the store and distinct from payload encoding —
a text envelope that base64s binary inside a binary-capable medium is a
workaround, not a design. The storage fix moves entirely below the store
boundary; the wire keeps its own total spelling (decision 4).

`WithEncoder` (per-publish and per-constructor) survives unchanged: under
the presentation contract the publisher legitimately chooses the
presentation; consumers dispatch on the tag per event in any case.

## Test matrix — every supported combination round-trips

Every cell is pinned by a test asserting verbatim payload bytes and the
original tag after the round trip; golden tests additionally pin the exact
spelling so a layer leak fails mechanically.

| Combination | Round-trip asserted | Where |
|---|---|---|
| JSON spelling × JSON payload | marshal embeds raw JSON (golden literal) → unmarshal verbatim | `we` spelling tests (13′) |
| JSON spelling × binary payload | marshal emits base64 (golden literal) → unmarshal verbatim | `we` spelling tests (13′) |
| CBOR spelling × JSON payload | native byte string both ways | `we` spelling tests (13′) |
| CBOR spelling × binary payload | native byte string both ways | `we` spelling tests (13′) |
| property: any `{encoding, bytes}` | spell→parse = identity in both mediums (rapid, ADR-0009) | `we` spelling tests (13′) |
| JSON wire × JSON payload command | wire-literal request → decoded payload (restored werestate literal; wehttp) | edge tests (13′/14a′) |
| JSON wire × binary payload command | base64-spelled payload end to end | edge tests (13′/14a′) |
| CBOR wire × any payload command | `application/cbor` body end to end | wehttp test (14a′) |
| CBOR response | `Accept: application/cbor` → CBOR body value-equal to the JSON rendering, correct Content-Type | wehttp test (14b′) |
| each backend × JSON payload | publish→load verbatim bytes + tag | conformance suite (16′) |
| each backend × CBOR payload | publish→load verbatim bytes + tag | conformance suite (16′) |

The conformance rows run on all five backends (memory, ds, jetstream,
kurrent, sqlite) through the shared validation suite.

## Revised tasks (replacing Tasks 13–16)

- **Task 13′ — Retype the presentation contract + canonical JSON spelling.**
  `we.Data.Data` → `[]byte` with a doc comment naming the presentation
  contract and the verbatim rule; canonical `MarshalJSON`/`UnmarshalJSON`
  implementing the JSON-wire spelling (raw-embedded JSON for JSON payload
  bytes, base64 for binary); golden wire-spelling tests pinning both JSON
  cells of the matrix — the werestate wire-literal test is restored, not
  weakened. Flip the ds/jetstream CBOR-override loud-failure tests to
  round-trips (the retype is what changes their behaviour). Core suite +
  samples + connectors + both stores green.
- **Task 14′ — JetStream CBOR envelope.** Default marshaller → CBOR; delete
  `JSONMarshaller`; envelope-shape assertions updated.
- **Task 14a′ — wehttp CBOR wire intake.** Accept `application/cbor`
  request bodies for commands (decode the same `RemoteCommand` envelope via
  fxamacker); responses stay JSON within this task.
- **Task 14b′ — wehttp CBOR responses.** Render resources (and structured
  rejection bodies) per the negotiated `Accept` medium from the same
  resource map the JSON rendering uses: `application/cbor` → CBOR body and
  Content-Type; everything else (absent, `*/*`, `application/json`) → JSON.
  Accept parsing is a media-range scan without q-values (documented
  simplification).
- **Task 15′ — ds binary attribute.** `ChangeSet.Events string` → `[]byte`
  (CBOR via fxamacker); read path `json.Unmarshal` → `cbor.Unmarshal`; flip
  the ds loud-failure test to a round-trip.
- **Task 16′ — Conformance scenario + sweep.** Suite scenario "round-trips
  cbor payloads through storage" across all five backends asserting verbatim
  bytes and original tag; remove the BLOB-scoping caveats from both store
  godocs and feature 08; update feature 08 verification rows; delete the
  item-A roadmap bullet; layer-naming doc comments on the artifacts agents
  touch (`we.Data`, the `Marshaller` seam, the edge handlers); an "encoding
  layers" section in CLAUDE.md with the layer-ownership litmus question.
  ADR-0011 is already committed and amended alongside this design. Phase
  gate: lint, full uncached suite 0 skips with Docker, restate integration,
  CodeRabbit.

## Decisions (all owner-confirmed, 2026-06-10)

1. The layer model, the verbatim rule, and `WithEncoder` retained.
2. Orphaning: previously stored ds/jetstream development events become
   unreadable (envelope layout changes). Orphan, not migrate.
3. `JSONMarshaller` deletion; the `Marshaller` seam stays for future
   store-local layouts.
4. Wire totality with CBOR encouraged: the JSON wire base64s binary payload
   bytes (its only total spelling); binary wires carry them natively;
   storage-encoding optimality is the store's concern, distinct from
   payload encoding ("never base64" is scoped away — at rest it vanishes by
   optimality, on the JSON wire it is the documented spelling).
