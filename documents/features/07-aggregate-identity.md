# Feature 07 — Aggregate Identity: Canonical Form, Validated Construction, Edge Parsing

- **Status:** Done · **Size:** M · **Area:** core (`we/`) + all stores + both connectors
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

- [ADR-0010](../adr/0010-identity-grammar.md) — canonical `type:key` form, identity
  invariants, edge parsing, and the durable-reference contract.
- [ADR-0009](../adr/0009-property-based-testing-rapid.md) — property-based testing via
  `pgregory.net/rapid` for the codec properties and the generative conformance
  scenario.

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
- **IDENTITY-S1.R4** (ubiquitous) — The parts shall conform to the normative
  grammar in [`documents/spec/aggregate-identity.md`](../spec/aggregate-identity.md)
  (grammar v2, [ADR-0010](../adr/0010-identity-grammar.md)): types are
  lowercase kebab tokens, letter-first, ≤ 64 octets; keys are pipe-joined
  segments of `[A-Za-z0-9._@-]`, ≤ 512 octets, never `.` or `..` as a whole.
  The grammar is defined by identity-domain concerns alone — legibility,
  lossless encodability, non-ambiguity — never by any store's transport
  (stores adapt to the key space; IDENTITY-S4).
- **IDENTITY-S1.R5** (unwanted) — If `aggregateType` violates R4, then `MakeAggregateId`
  shall return `Reason: "invalid-type"`; if `key` violates R4, `Reason: "invalid-key"`
  — each with a zero `AggregateId`.
- **IDENTITY-S1.R6** (ubiquitous) — `InvalidAggregateIdError` shall carry the offending
  `Type`, `Key`, and a `Reason` drawn from exactly the set `{"empty-type", "empty-key",
  "invalid-type", "invalid-key", "missing-separator"}` so callers can classify without
  string-matching messages.
- **IDENTITY-S1.R7** (ubiquitous) — All implementations bind to the shared
  grammar through the conformance vector file
  (`documents/spec/aggregate-identity.vectors.json`); per-implementation
  status is tracked in the spec's conformance table. Until an implementation
  aligns, identities it writes outside the grammar fail this parser loudly —
  an error, never a transformation.
- **IDENTITY-S1.R8** (ubiquitous) — `"|"` is the composite-key segment
  separator, formalised in the grammar (segments non-empty, pipes interior
  only); the framework shall treat segment content and count as opaque and
  never parse or interpret them. In strict URL contexts `"|"`
  percent-encodes deterministically (`%7C`) and the HTTP edge decodes path parameters,
  so both spellings reach `MakeAggregateId` as the same identity.

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
  that point at aggregates, and its grammar is frozen (see ADR-0010).

### IDENTITY-S3 — Parse identity at every edge

*As an API author, I want untrusted identity strings parsed through one canonical
decoder, so that an invalid identity is rejected at the boundary instead of flowing into
stores as if it were real.*

- **IDENTITY-S3.R1** (ubiquitous) — `EncodedAggregateId.Decode() (AggregateId, error)`
  shall parse at the **first** `":"` (`strings.Cut` semantics, mirroring Rust's
  `split_once`), build the result through the IDENTITY-S1 rules, and return the value
  form (signature change from today's `(*AggregateId, error)`). A key segment
  containing a further `":"` therefore fails charset validation (`"invalid-key"`).
- **IDENTITY-S3.R2** (unwanted) — If the input contains no `":"`, then `Decode` shall
  return a `*we.InvalidAggregateIdError` with `Reason: "missing-separator"`.
- **IDENTITY-S3.R3** (unwanted) — If the type or key segment is empty, then `Decode`
  shall return the corresponding `*we.InvalidAggregateIdError` (`"empty-type"` /
  `"empty-key"`), exactly as Rust's `AggregateIdParseError` distinguishes the cases.
- **IDENTITY-S3.R4** (ubiquitous) — `Decode(Encode(id)) == id` shall hold for every
  identity accepted by `MakeAggregateId`, enforced **property-based** (ADR-0009):
  identities generated across the full charset grammar round-trip exactly, and a
  companion rejection property shows that injecting any generated out-of-charset
  character into either part yields the correct `invalid-type`/`invalid-key` reason.
  Failures shrink to minimal counterexamples.
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

- **IDENTITY-S4.R1** (ubiquitous) — Each store shall derive its storage key (DynamoDB
  partition key, JetStream subject, Kurrent stream id, …) from
  `AggregateId.Encode()`; no backend shall maintain its own identity spelling. Where a
  transport cannot carry the canonical form verbatim, the store shall apply its own
  **deterministic, lossless, store-local encoding** — invisible to callers, reversed
  on read.
- **IDENTITY-S4.R2** (unwanted) — A store shall never reject, truncate, or lossily
  transform a valid identity, and shall never constrain the identity contract:
  **stores adapt to the key space; the key space never adapts to a store.** Stores
  come and go with distinct storage and encodings — the contract cannot bake in any
  transport's limitations, including unknown future ones. *(Incidentally, most
  backends carry the charset verbatim — `|` is an ordinary NATS token character —
  while JetStream applies the R1 store-local encoding for `.`, the NATS token
  separator. Either way that is an observation, not the contract; the R3 scenario
  is what binds every present and future store.)*
- **IDENTITY-S4.R3** (event-driven) — When aggregates with **generated** identities
  (full charset grammar, including composite `|` keys) and **generated** payload
  content (arbitrary unicode strings, including empty) are published and loaded,
  every backend shall return events whose `AggregateId` equals the published identity
  and whose payload decodes to the published values. *(New property-based
  conformance-suite scenario — ADR-0009, rapid's default 100 checks per backend —
  registered in `scenarios()` so it runs against memory, ds, jetstream, kurrent, and
  sqlite automatically. This is the binding form of R2 for stores that do not exist
  yet.)*
- **IDENTITY-S4.R4** (unwanted) — If the canonical-form change alters a backend's
  derived keys (it does — previously dot-joined), then previously written dot-keyed
  development data is orphaned, not migrated. *(Owner decision: correctness over
  backward compatibility; the port is unreleased. Recorded in ADR-0010.)*

## Out of scope

- Projection/document machinery itself (roadmap phase) — this feature only freezes the
  reference format that phase will consume.
- Unexported-field `AggregateId` with mandatory constructor (rejected in ADR-0010 —
  ripples through every literal construction site for marginal gain over boundary
  parsing).

## Verification

| Requirement | Test |
|---|---|
| IDENTITY-S1.R1–R6 | Unit: table-driven `MakeAggregateId` cases — valid (full charsets, incl. composite `kevin\|card\|boots`), empty type/key, colon/space/percent in type and key, `\|` in type (rejected — key-only), `"."`/`".."` parts; assert `*InvalidAggregateIdError` with exact `Reason` via `errors.As` |
| IDENTITY-S1.R7, S1.R8 | Conformance vector suites in Go (`we/identity-vectors_test.go`) and Rust (vendored verbatim copy); per-implementation status in the spec's conformance table; composite convention formalised in the grammar, no segment parsing anywhere |
| IDENTITY-S2.R1, S2.R2 | Unit: `Encode` golden values; cross-checked against Rust `Display` fixtures (`counter:live-1`) |
| IDENTITY-S2.R3 | `we/resource-encoder_test.go` asserts the canonical `$id` form (`"counter:a"`) in encoded responses — wehttp delegates to this pure encoder; werestate's integration test asserts `$id == "counter:live-1"` directly |
| IDENTITY-S3.R1–R4 | Unit: decode error table (missing separator, empty parts, colon-bearing key → `invalid-key`); rapid property tests — grammar-generated round-trip + out-of-charset rejection (ADR-0009) |
| IDENTITY-S3.R5, S3.R6 | wehttp test: request with colon-bearing type and space-bearing key segments → 400 `"invalid aggregate id"`, stub service never invoked |
| IDENTITY-S3.R7 | Existing `EncodeKey`/`decodeKey` tests pass against the delegating implementation; the former colon-in-key round-trip flips to a rejection test (`invalid-key`) |
| IDENTITY-S4.R1, S4.R3 | New property-based conformance scenario `IdentityRoundTripsThroughStorage` (rapid-generated identities + payloads) registered in `scenarios()`; green on all five backends (Docker for ds/jetstream/kurrent) |
| IDENTITY-S4.R2 | The S4.R3 scenario is the binding check for every store, present and future — a store that rejects or mangles any valid identity fails it; ADR-0010 records the adaptation contract |
