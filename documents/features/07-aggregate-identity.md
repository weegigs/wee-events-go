# Feature 07 — Aggregate Identity: Canonical Form, Validated Construction, Edge Parsing

- **Status:** Ready · **Size:** M · **Area:** core (`we/`) + all stores + both connectors
- **Coordinates with:** [Feature 09](09-error-surfacing.md) (shared `connectors/werestate/restate.go`, `connectors/wehttp/http.go`) · [Feature 08](08-explicit-event-encoding.md) (shared store files, disjoint functions)
- **Prefix:** `IDENTITY`

## Summary

Give aggregate identity one canonical string form, one set of validity rules, and one
parser — and make every place identity enters or leaves the system use them.

Today identity is fragmented: `AggregateId.Encode()` renders `type.key` (inherited from
the TypeScript original), the Restate connector hand-rolls a separate `type:key` codec,
the HTTP connector builds `AggregateId` from URL path segments with **no validation at
all**, and `EncodedAggregateId.Decode()` is broken (splits on every separator) and dead.
The reference implementation — `wee-events.rs` — renders `Display` as `type:key` and
parses with `split_once(':')` plus typed errors. Because nothing validates `Type`, two
distinct identities can collide on one storage key (`{foo.bar, baz}` and `{foo, bar.baz}`
both derive `foo.bar.baz`) — silent data merging.

After this feature: the canonical encoded form is **`<type> ":" <key>`** matching the
Rust sibling byte-for-byte; `MakeAggregateId` is the single validating constructor;
`EncodedAggregateId.Decode` is the canonical boundary parser; every backend derives its
storage key from the canonical form; and the encoded form is the declared durable
reference format for the future projections phase. Correctness is prioritised over
compatibility with previously written dot-form keys (owner decision — the port is
unreleased).

## Decisions

- [ADR-0008](../adr/0008-aggregate-identity.md) — canonical `type:key` form, identity
  invariants, edge parsing, and the durable-reference contract.

## User stories

### IDENTITY-S1 — Construct identity through one validating constructor

*As a framework user, I want a single constructor that rejects invalid aggregate
identities, so that an identity that exists in the system is valid by construction and
every derived key is injective.* (Parse, don't validate — principle 3.)

- **IDENTITY-S1.R1** (ubiquitous) — The framework shall provide
  `MakeAggregateId(aggregateType string, key string) (AggregateId, error)` as the
  validating constructor for untrusted input.
- **IDENTITY-S1.R2** (unwanted) — If `aggregateType` is empty, then `MakeAggregateId`
  shall return a `*we.InvalidAggregateIdError` with `Reason: "empty-type"` and a zero
  `AggregateId`.
- **IDENTITY-S1.R3** (unwanted) — If `key` is empty, then `MakeAggregateId` shall return
  a `*we.InvalidAggregateIdError` with `Reason: "empty-key"` and a zero `AggregateId`.
- **IDENTITY-S1.R4** (unwanted) — If `aggregateType` contains the separator `":"`, then
  `MakeAggregateId` shall return a `*we.InvalidAggregateIdError` with
  `Reason: "type-contains-separator"` and a zero `AggregateId`. *(A colon-bearing type
  would make the canonical form ambiguous. The key may contain colons — the first
  separator is authoritative.)*
- **IDENTITY-S1.R5** (ubiquitous) — `InvalidAggregateIdError` shall carry the offending
  `Type`, `Key`, and a `Reason` drawn from exactly the set `{"empty-type", "empty-key",
  "type-contains-separator", "missing-separator"}` so callers can classify without
  string-matching messages.

### IDENTITY-S2 — Render the canonical encoded form

*As an integrator, I want one canonical string spelling of an aggregate identity, so that
ids in API responses, logs, and stored references are the same string the Rust sibling
produces for the same identity.*

- **IDENTITY-S2.R1** (ubiquitous) — `AggregateId.Encode()` shall render
  `<type> ":" <key>` — byte-for-byte equal to `wee-events.rs`'s
  `impl Display for AggregateId`.
- **IDENTITY-S2.R2** (ubiquitous) — `Encode` shall remain infallible: every
  `AggregateId` accepted by `MakeAggregateId` encodes without error.
- **IDENTITY-S2.R3** (event-driven) — When the HTTP connector or the Restate connector
  serialises an entity response, the `$id` field shall carry the canonical encoded form.
- **IDENTITY-S2.R4** (ubiquitous) — The doc comment on `EncodedAggregateId` shall state
  the durable-reference contract: the encoded form is the reference format for documents
  that point at aggregates, and its grammar is frozen (see ADR-0008).

### IDENTITY-S3 — Parse identity at every edge

*As an API author, I want untrusted identity strings parsed through one canonical
decoder, so that an invalid identity is rejected at the boundary instead of flowing into
stores as if it were real.*

- **IDENTITY-S3.R1** (ubiquitous) — `EncodedAggregateId.Decode() (AggregateId, error)`
  shall parse at the **first** `":"` (`strings.Cut` semantics, mirroring Rust's
  `split_once`), build the result through the IDENTITY-S1 rules, and return the value
  form (signature change from today's `(*AggregateId, error)`).
- **IDENTITY-S3.R2** (unwanted) — If the input contains no `":"`, then `Decode` shall
  return a `*we.InvalidAggregateIdError` with `Reason: "missing-separator"`.
- **IDENTITY-S3.R3** (unwanted) — If the type or key segment is empty, then `Decode`
  shall return the corresponding `*we.InvalidAggregateIdError` (`"empty-type"` /
  `"empty-key"`), exactly as Rust's `AggregateIdParseError` distinguishes the cases.
- **IDENTITY-S3.R4** (ubiquitous) — `Decode(Encode(id)) == id` shall hold for every
  identity accepted by `MakeAggregateId`, enforced by a round-trip property test that
  includes keys containing `":"` and `"."`.
- **IDENTITY-S3.R5** (event-driven) — When the HTTP connector receives `{type}` and
  `{key}` path parameters, it shall build the identity via `MakeAggregateId` for both
  `getResource` and `executeCommand`.
- **IDENTITY-S3.R6** (unwanted) — If the path parameters are invalid, then the HTTP
  connector shall respond `400` with the static body `"invalid aggregate id"` and shall
  not invoke the entity service. *(Failure companion to R5: nothing invalid reaches a
  store.)*
- **IDENTITY-S3.R7** (ubiquitous) — The Restate connector's object key codec shall *be*
  the canonical codec: `EncodeKey` validates via `MakeAggregateId` and returns
  `Encode()`'s output; `decodeKey` delegates to `EncodedAggregateId.Decode`. The
  hand-rolled separator logic is deleted. Decode failures remain terminal bad-request
  errors (RESTATE-S3 taxonomy unchanged).

### IDENTITY-S4 — Derive every storage key from the canonical form

*As an operator, I want each backend's storage key derived from the one canonical
encoding, so that the same aggregate maps to the same key family everywhere and no
backend can silently merge two identities.*

- **IDENTITY-S4.R1** (ubiquitous) — The DynamoDB partition key, the JetStream subject
  (`prefix + Encode()`), and the Kurrent stream id shall derive from
  `AggregateId.Encode()`; no backend shall maintain its own identity spelling.
- **IDENTITY-S4.R2** (unwanted) — If a backend's transport cannot represent a valid
  identity (for example a NATS-illegal character in a subject token), then the store
  shall surface the transport's rejection as an error from `Publish`/`Load` — it shall
  never transform, truncate, or substitute the identity.
- **IDENTITY-S4.R3** (event-driven) — When an aggregate whose key contains `":"` and
  `"."` is published and loaded, every backend shall return events whose
  `AggregateId` equals the published identity. *(New conformance-suite scenario; runs
  against memory, ds, jetstream, kurrent, and sqlite via `Run(t)` auto-registration.)*
- **IDENTITY-S4.R4** (unwanted) — If the canonical-form change alters a backend's
  derived keys (it does — previously dot-joined), then previously written dot-keyed
  development data is orphaned, not migrated. *(Owner decision: correctness over
  backward compatibility; the port is unreleased. Recorded in ADR-0008.)*

## Out of scope

- Projection/document machinery itself (roadmap phase) — this feature only freezes the
  reference format that phase will consume.
- Constraining `Type` beyond the separator rule (no charset/kebab-case enforcement);
  kebab-case stays a documented convention, as in Rust's `AggregateType`.
- Unexported-field `AggregateId` with mandatory constructor (rejected in ADR-0008 —
  ripples through every literal construction site for marginal gain over boundary
  parsing).

## Verification

| Requirement | Test |
|---|---|
| IDENTITY-S1.R1–R5 | Unit: table-driven `MakeAggregateId` cases — valid, empty type, empty key, colon-in-type; assert `*InvalidAggregateIdError` with exact `Reason` via `errors.As` |
| IDENTITY-S2.R1, S2.R2 | Unit: `Encode` golden values incl. key containing `":"`; cross-checked against Rust `Display` fixtures (`counter:live-1`) |
| IDENTITY-S2.R3 | wehttp + werestate response tests assert `$id == "counter:live-1"` form |
| IDENTITY-S3.R1–R4 | Unit: decode error table mirroring Rust `AggregateIdParseError`; round-trip property test over generated ids with separator-bearing keys |
| IDENTITY-S3.R5, S3.R6 | wehttp test: request with empty/colon-bearing type segment → 400 `"invalid aggregate id"`, stub service never invoked |
| IDENTITY-S3.R7 | Existing `EncodeKey`/`decodeKey` tests pass against the delegating implementation; colon-in-key round-trip retained |
| IDENTITY-S4.R1, S4.R3 | New conformance scenario `IdentityRoundTripsThroughStorage` registered in `scenarios()`; green on all five backends (Docker for ds/jetstream/kurrent) |
| IDENTITY-S4.R2 | jetstream: publish with a NATS-rejectable identity asserts a surfaced error, not a mangled subject |
