# Feature 08 — Explicit Event Encoding (No Implicit Default)

- **Status:** Ready · **Size:** M · **Area:** core (`we/`) + all stores + samples/wiring
- **Coordinates with:** [Feature 07](07-aggregate-identity.md) (shared store files,
  disjoint functions — encoding seam vs key derivation)
- **Prefix:** `ENCODING`

## Summary

Remove the implicit write-encoding default. Today `we.MarshalToData` silently falls back
to the JSON encoder, so every store writes JSON without any caller ever having chosen
it — a hidden package-level default ([ADR-0001](../adr/0001-default-event-encoding-json.md)).
The owner's direction: **all defaults must be explicit** — whoever constructs a store
states its write encoding in code.

After this feature `MarshalToData` requires an `Encoder` argument, every store
constructor takes an explicit `we.Encoder`, and there is no code path that selects an
encoding the caller did not name. JSON remains the *recommended* interop encoding —
recommended in [ADR-0007](../adr/0007-explicit-event-encoding.md), never assumed in
code. The read path is untouched: decoding already dispatches explicitly on the
envelope's declared `encoding` discriminator (Feature 01).

## Decisions

- [ADR-0007](../adr/0007-explicit-event-encoding.md) — encoding is an explicit
  constructor argument; supersedes ADR-0001 (which carried the implicit default). The
  interop rationale of ADR-0001 (JSON for cross-family byte-compatibility) survives as
  the recommendation.

## User stories

### ENCODING-S1 — No implicit encoder anywhere in the core

*As the framework owner, I want the core to be incapable of choosing an encoding on its
own, so that reading any call site tells me exactly what bytes it writes.*

- **ENCODING-S1.R1** (ubiquitous) — `MarshalToData` shall have the signature
  `MarshalToData(encoder Encoder, value any) (Data, error)`; the parameterless implicit
  selection is deleted.
- **ENCODING-S1.R2** (ubiquitous) — The `we` package shall export no package-level
  default encoder value and no function that selects an encoder the caller did not pass.
  *(Compile-enforced: removing the default makes an unnamed encoding a build error, not
  a runtime surprise.)*
- **ENCODING-S1.R3** (state-driven) — While decoding, `UnmarshalFromData` shall continue
  to dispatch on the envelope's declared `encoding` via the `Decoders` registry,
  returning `*UnknownEncodingError` for an unregistered encoding (unchanged from
  CBOR-S2; explicit by data, not by default).

### ENCODING-S2 — Stores declare their write encoding at construction

*As a service author, I want to state a store's write encoding when I build the store,
so that the encoding of a stream is a reviewable line of code and cannot vary
call-by-call.*

- **ENCODING-S2.R1** (ubiquitous) — Each store constructor (`stores/ds`,
  `stores/jetstream`, `stores/kurrent`, `stores/sqlite`, and the in-memory test store)
  shall accept an explicit `we.Encoder` and use it for every event it encodes.
- **ENCODING-S2.R2** (unwanted) — If the encoder argument is `nil`, then the constructor
  shall return an error of the form `"<store>: encoder is required"` and no store value
  — never a deferred nil-dereference at first publish. *(Failure companion to R1.)*
- **ENCODING-S2.R3** (ubiquitous) — Encoding shall be a per-store-instance decision
  only: `Publish` shall offer no per-call encoding override. *(A stream's encoding must
  not vary call-by-call — ADR-0007.)*
- **ENCODING-S2.R4** (event-driven) — When samples, Wire injectors, and the conformance
  suite construct stores, they shall pass `we.MakeJSONEncoder()` explicitly,
  demonstrating the recommended interop choice.

### ENCODING-S3 — Explicitness changes no bytes for JSON writers

*As an operator of a mixed Go/Rust deployment, I want the explicitness refactor to be
byte-neutral when JSON is chosen, so that interop and existing behaviour are provably
unchanged.*

- **ENCODING-S3.R1** (ubiquitous) — With `we.MakeJSONEncoder()` supplied, the encoded
  `Data` (bytes and `encoding` discriminator) shall be byte-identical to the
  pre-feature output. *(CBOR-S3.R2's byte-compatibility test is the oracle and must
  remain green unmodified.)*
- **ENCODING-S3.R2** (event-driven) — When a store constructed with the CBOR encoder
  publishes, the envelope shall carry `application/cbor` and the conformance suite's
  opaque-payload guarantees (SQLITE-S4, CONFORMANCE) shall hold unchanged. *(End-to-end
  CBOR remains scoped to BLOB-backed stores per the recorded Feature 01 follow-up; the
  loud `json.Marshal` failure on ds/jetstream is pre-existing, verified behaviour.)*

## Out of scope

- Per-publish or per-event encoding selection (explicitly rejected — ENCODING-S2.R3).
- Changing the default *recommendation* away from JSON (interop rationale unchanged).
- Payload encryption (Feature 06) and any new codec.

## Verification

| Requirement | Test |
|---|---|
| ENCODING-S1.R1, S1.R2 | Compile-level: all call sites updated; grep-verifiable absence of a package default; unit tests construct `MarshalToData(MakeJSONEncoder(), …)` |
| ENCODING-S1.R3 | Existing CBOR-S2 decoder-registry tests pass unchanged |
| ENCODING-S2.R1, S2.R4 | All store constructors compile with the new parameter; suite + samples pass `MakeJSONEncoder()`; full uncached test run green on all 10 packages |
| ENCODING-S2.R2 | Per-store unit test: `nil` encoder → constructor error containing `"encoder is required"`, no store returned |
| ENCODING-S3.R1 | `jsonEncoderPreservesPreFeatureBytes` (CBOR-S3.R2) green unmodified |
| ENCODING-S3.R2 | sqlite store constructed with `MakeCBOREncoder()`: publish/load round-trip asserts `application/cbor` discriminator and verbatim bytes |
