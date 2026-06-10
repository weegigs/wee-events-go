# ADR-0008 — Aggregate identity: canonical `type:key` form and validated construction

- **Status:** Accepted
- **Relates to:** [features/07-aggregate-identity.md](../features/07-aggregate-identity.md) · [features/09-error-surfacing.md](../features/09-error-surfacing.md)

## Context

Aggregate identity is fragmented across the port. `AggregateId.Encode()` renders
`type.key` — inherited from the TypeScript original (`core.ts`:
`` `${aggregate.type}.${aggregate.key}` ``) — while the reference implementation this
port tracks, `wee-events.rs`, renders `Display` as **`type:key`** and parses with
`split_once(':')` plus typed errors (`MissingColon`, `EmptyType`, `EmptyKey`). The
Restate connector hand-rolls its own `type:key` codec; the HTTP connector constructs
identities from URL segments with no validation; `EncodedAggregateId.Decode()` splits on
every separator (broken for dotted keys) and is dead code. Colon namespacing is the
family-wide convention elsewhere too (command names `counter:increment`, entity types
`counter:counter`).

Nothing validates `Type`, so two distinct identities can derive the same storage key
(`{foo.bar, baz}` and `{foo, bar.baz}` → `foo.bar.baz`): silent data merging, the worst
class of failure (a value that looks correct and is not).

The owner has ruled: correctness over backward compatibility — the port is unreleased,
and previously written dot-keyed development data does not constrain the design.

## Decision

1. **Canonical encoded form:** `<type> ":" <key>`, byte-for-byte equal to Rust's
   `Display`. The first colon is the separator; keys may contain colons.
2. **Identity invariants** (exactly Rust's rules): `Type` non-empty and colon-free;
   `Key` non-empty. Enforced by the single validating constructor
   `MakeAggregateId(type, key) (AggregateId, error)` returning a typed
   `*InvalidAggregateIdError` with a closed `Reason` set.
3. **One parser:** `EncodedAggregateId.Decode()` is the canonical boundary parser
   (first-separator `Cut`, the same typed errors). Every edge that receives identity as
   a string parses through it or through `MakeAggregateId` — the HTTP path parameters,
   the Restate object key (whose hand-rolled codec collapses into the canonical one).
4. **`Encode` stays infallible** and every backend derives its storage key from it
   (DynamoDB partition key, JetStream subject, Kurrent stream id). A backend whose
   transport rejects a valid identity surfaces that rejection as an error; it never
   transforms the identity.
5. **Durable-reference contract:** the canonical encoded form is the reference format
   for documents that point at aggregates (the projections phase consumes this). Its
   grammar and invariants are therefore frozen: loosening is permitted, tightening is
   not, because persisted references must decode forever.

## Consequences

- Identity validity is established at the boundary once; everything downstream —
  storage keys, `$id` fields, persisted references — is decodable by construction. A
  reference that fails `Decode` is a true corruption signal.
- `$id` in HTTP and Restate responses changes spelling (`counter.live-1` →
  `counter:live-1`), matching what the Rust connector serves. Backend storage keys
  change shape the same way; previously written dot-keyed development data is orphaned,
  not migrated (owner decision).
- The Go and Rust implementations accept and reject identical identity strings — parity
  is testable with shared fixtures.
- The struct JSON form (`{"type":…,"key":…}`) inside stored event envelopes is
  untouched; this ADR governs the *string* spelling only.
- The Restate `type:key` object key and the canonical form become the same thing — one
  codec, one implementation, one test surface.

## Alternatives considered

- **Canonize the existing dot form.** Rejected: it diverges from the Rust reference the
  port exists to track, conflicts with the family's colon-namespacing convention, and
  would freeze the TypeScript spelling as the contract while the Rust `FromStr` rejects
  it. Compatibility with already-written dot keys was the only argument, and the owner
  has ruled correctness over compatibility.
- **Unexported fields + mandatory constructor** (illegal states unrepresentable).
  Rejected: ripples through every literal construction site in stores, connectors,
  samples, and tests for marginal gain over boundary parsing; Rust makes the same
  trade (`new()` trusts typed parts, `FromStr` validates untrusted strings).
- **Per-backend identity spellings, frozen for compatibility.** Rejected: complexity
  without a correctness benefit once compatibility is off the table; one canonical
  derivation is strictly simpler to reason about and test.
- **Constraining `Type` to a charset (kebab-case enforcement).** Deferred: loosening a
  frozen contract is safe, tightening is not — so the contract ships with the minimal
  separator rule, and kebab-case stays a documented convention as in Rust.
