# Aggregate Identity Charset — Shared Normative Specification (Design)

**Date:** 2026-06-10 · **Status:** Approved by owner · **Size:** S–M

## Decision

The aggregate identity grammar moves from four drift-prone locations (IDENTITY-S1
requirements, ADR-0008 prose, `we/aggregate-id.go` constants, `we/identity-gen.go`
regexes) into one canonical, machine-checkable package in this repository:

1. `documents/spec/aggregate-identity.md` — normative spec (prose rules + ABNF).
2. `documents/spec/aggregate-identity.vectors.json` — conformance vectors, the
   cross-implementation contract.
3. `we/identity-vectors_test.go` — Go consumes the vectors directly.
4. `documents/writing-documents.md` — the document-writing standard adopted for
   ecosystem documents (captured as part of this work; see §6).

All three implementations are bound: Go (conformant today), Rust `wee-events.rs`
(pending — roadmap follow-up "align wee-events.rs to the tightened identity
charset"), TypeScript `wee-events` (pending — still emits the legacy `type.key`
dot form). Rust and TypeScript reconcile to this spec; the spec never adapts to
them. No Go validation behaviour changes — the implementation already is the
grammar; this work gives it a canonical home.

## Owner-settled questions

- **Binding scope:** all three implementations (Go, Rust, TypeScript) share one
  identifier space and reconcile to the spec.
- **Home:** this repository is the current home for ecosystem-wide documents.
- **Format:** prose + ABNF + JSON conformance vectors, edited to the
  "Do Not Ship the AI Draft" standard (rules only; rationale stays in ADR-0008).

## 1. The spec document

`documents/spec/aggregate-identity.md` states rules only; every rationale cites
[ADR-0008](../../../documents/adr/0008-aggregate-identity.md). Content, in
cut-from-the-bottom order:

- **Canonical form:** `<type> ":" <key>`; parse at the first colon. Under the
  charsets below the canonical form contains exactly one colon.
- **Charsets:** type = RFC 3986 unreserved (`A-Z a-z 0-9 - . _ ~`); key =
  unreserved plus `|`.
- **Part rules:** both parts non-empty; neither may be `.` or `..`; pure ASCII;
  **no length limit** (the Go validator enforces none; the spec must not invent
  constraints the implementation does not enforce).
- **Key opacity:** `|` is the documented composite-key segment convention
  (`kevin|card|boots`); no implementation parses key segments.
- **Closed rejection-reason set:** `empty-type`, `empty-key`, `invalid-type`,
  `invalid-key`, `missing-separator`. Implementations map native error types
  onto these identifiers.
- **Frozen-grammar rule:** loosening is permitted, tightening is not; persisted
  references must decode forever. Changes bump the vector-file version.
- **Stores-adapt rule:** stores carry the canonical form verbatim where the
  transport allows, otherwise apply a deterministic, lossless, store-local
  encoding (e.g. JetStream `.` → `%2E`). A store never rejects, truncates, or
  constrains a valid identity.
- **URL carriage note:** in strict URL contexts only `|` percent-encodes
  (`%7C`); HTTP edges decode path parameters before parsing.
- **Conformance:** what an implementation must assert against the vector file
  (every vector, plus byte-for-byte re-encode round-trip for valid parse
  vectors), and an implementation-status table: Go conformant; Rust pending;
  TypeScript pending migration from the dot form.

The ABNF covers the grammar; the `.`/`..` exclusion stays in prose, where it is
clearer than ABNF contortions.

## 2. Vector file schema

Two groups, matching the two validation boundaries every implementation exposes
(construct from parts; parse an encoded string):

```json
{
  "spec": "aggregate-identity",
  "version": 1,
  "construct": [
    {"type": "counter", "key": "live-1", "valid": true},
    {"type": "counter", "key": "a:b", "valid": false, "reason": "invalid-key"}
  ],
  "parse": [
    {"input": "counter:live-1", "valid": true, "type": "counter", "key": "live-1"},
    {"input": "counter", "valid": false, "reason": "missing-separator"},
    {"input": "kevin|card:boots", "valid": false, "reason": "invalid-type"}
  ]
}
```

Coverage requirements:

- Every rejection reason at both boundaries where applicable
  (`missing-separator` is parse-only).
- Charset edges: `~` in both parts; `|` valid in key, invalid in type; `.` and
  `..` rejected as whole parts but valid inside parts; `%`, `:`, `/`,
  whitespace, and non-ASCII rejected; empty string and lone-`:` forms.
- The documented composite-key example (`card:kevin|card|boots` valid).
- Valid parse vectors round-trip: re-encoding the parsed parts reproduces the
  input bytes exactly.

Vectors pin boundary cases and cross-language agreement; the existing rapid
generators (`we/identity-gen.go`, ADR-0009) remain Go's generative layer above
them.

## 3. Versioning and vendoring

The `version` integer is the entire mechanism. Rust and TypeScript vendor a
verbatim copy of the vector file into their test trees; each consumer's
conformance test asserts `version == <expected>`, so a stale copy fails visibly
when the master moves. Updating a consumer = copy the new file, bump the
expectation, fix what the new vectors catch. No checksums, submodules, or
network fetches — the grammar is frozen loosen-only, so version bumps are rare
and deliberate.

## 4. Go conformance test

`we/identity-vectors_test.go` loads `../documents/spec/aggregate-identity.vectors.json`
and asserts, for every vector: `MakeAggregateId` outcomes (construct group),
`EncodedAggregateId.Decode` outcomes (parse group), the specific
`InvalidAggregateIdError.Reason` against the vector's `reason`, and the
round-trip rule. Test failures name the offending vector input.

## 5. Cross-reference sweep (drift retirement)

The four current grammar locations stop being normative and point at the spec:

| Location | Change |
|---|---|
| `documents/adr/0008-aggregate-identity.md` | "Relates to" line names the spec as the normative grammar; decision text untouched (ADR stays the *why*, spec is the *what*) |
| `documents/features/07-aggregate-identity.md` | IDENTITY-S1.R4/R7/R8 gain a cross-reference to the spec |
| `we/aggregate-id.go` | Charset-constant comments cite the spec instead of restating the grammar |
| `we/identity-gen.go` | Generator comments cite the spec |

The roadmap follow-up for Rust alignment (item D) is updated to reference the
spec and its vendored vectors as the implementation contract.

## 6. Writing standard

`documents/writing-documents.md` captures the owner-endorsed "Do Not Ship the
AI Draft" standard, put through its own two-pass edit: core rule, document
order (point → decision → reasoning → trade-offs → detail), the
regenerable-content filter, voice and specificity rules, length budgets, and
the pre-publish checklist. Two house adaptations: objective third-person voice
(per CLAUDE.md, overriding the guide's first-person examples), and an explicit
carve-out for normative reference documents, which optimise for precision and
lookup rather than narrative brevity. `documents/conventions.md` gains a
one-line pointer.

## Out of scope

- Rust and TypeScript code changes (roadmap item D and the future TypeScript
  migration consume this spec; they do not ship with it).
- Any change to Go validation behaviour.
- Promotion to a separate spec repository — deferred until a second
  cross-implementation contract exists; the vendoring mechanism is unchanged by
  any future move.

## Testing

- The Go vector test is the acceptance gate for the spec/vector content itself:
  if a vector disagrees with `we/aggregate-id.go`, the vector (or the spec) is
  wrong — the implementation is the current source of truth being formalised.
- Existing identity tests (unit, rapid properties, store conformance) must stay
  green untouched — this work adds tests and documents only.
- Lint and full-suite gates per house rules.
