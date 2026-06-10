# Aggregate Identity Charset — Shared Normative Specification (Design)

**Date:** 2026-06-10 · **Status:** Approved by owner (revised — grammar v2) · **Size:** M

## Decision

The aggregate identity grammar is revised (a pre-release tightening — the last
safe moment under the frozen-grammar rule) and moves into one canonical,
machine-checkable package in this repository:

```abnf
identity = type ":" key
type     = word *( "-" token )              ; 1–64 octets
word     = lower *( lower / digit )         ; first token starts with a letter
token    = 1*( lower / digit )
key      = segment *( "|" segment )         ; 1–512 octets total
segment  = 1*( ALPHA / DIGIT / "-" / "." / "_" / "@" )
```

Plus one normative prose rule the ABNF cannot express cleanly: **the key as a
whole is never `.` or `..`** (a part used as a URL path component would be
dot-segment-normalised away). The rule is whole-key only — `..` inside a key
(`a|..|b`, `v1..2`) is opaque data and legal; the hazard exists only when the
entire path component is a dot-segment.

The two separators are treated identically: `-` joins non-empty tokens inside a
type exactly as `|` joins non-empty segments inside a key — no leading,
trailing, or doubled separators in either part, enforced by grammar shape.

Deliverables:

1. `documents/spec/aggregate-identity.md` — normative spec (prose rules + ABNF).
2. `documents/spec/aggregate-identity.vectors.json` — conformance vectors, the
   cross-implementation contract.
3. `documents/adr/0010-identity-grammar.md` — supersedes ADR-0008 (the charset
   is ADR-0008's decision; revising it requires supersession, not annotation).
4. Go implementation update: `we/aggregate-id.go`, `we/identity-gen.go`, and
   the identity tests enforce the revised grammar; `we/identity-vectors_test.go`
   consumes the vectors.
5. `documents/writing-documents.md` — the document-writing standard adopted for
   ecosystem documents (see §6).

All three implementations are bound: Go (brought to the revised grammar in this
work), Rust `wee-events.rs` (pending — roadmap item D), TypeScript `wee-events`
(pending — still emits the legacy `type.key` dot form). Rust and TypeScript
reconcile to this spec; the spec never adapts to them.

## Owner-settled questions

- **Binding scope:** all three implementations share one identifier space.
- **Home:** this repository is the current home for ecosystem-wide documents.
- **Format:** prose + ABNF + JSON conformance vectors; rules in the spec,
  rationale in the ADR, cases in the vectors.
- **Internationalisation:** keys stay ASCII. Unicode normalization (NFC vs NFD)
  would make visually identical keys address different aggregates — a silent
  split, mirror image of the dot-form silent merge. Deferred with a documented
  loosening path: non-ASCII letters/digits, NFC mandated, unnormalised input
  rejected (never silently normalised). Natural-language data belongs in
  payloads, which are full UTF-8 already.
- **Grammar revision (v2):** type tightened to lowercase kebab (`[a-z][a-z0-9-]*`)
  — types are code-authored schema names; case-confusable types are the same
  hazard family as the dot merge. `~` dropped from both parts (obscure to read,
  present only via RFC 3986 unreserved). `@` added to key segments (emails are
  common natural keys; `@` is legal raw in URL path segments per RFC 3986
  `pchar`). `|` formalised as the composite separator: interior-only, segments
  non-empty — keys remain semantically opaque (no implementation interprets
  segment content or count), but are syntactically well-formed by grammar.
- **Length caps:** type ≤ 64 octets, key ≤ 512. Worst-case canonical form is
  577 bytes (inside DynamoDB's 1,024-byte sort-key limit); worst-case
  URL-encoded form ≈ 1,100 characters (inside the ~2,000-character interop
  bound). The caps keep the forever-frozen grammar cheap for stores that do
  not exist yet.
- **Rejected expansions:** `+` (decoders disagree on space semantics), other
  sub-delims (read as prose or pattern syntax), colons in keys (re-opens the
  ambiguity the single-colon form eliminates; `run|01ABC` expresses the same
  composite within the grammar).

## 1. The spec document

`documents/spec/aggregate-identity.md` states rules only; every rationale cites
ADR-0010. Content, in cut-from-the-bottom order:

- **Canonical form:** `<type> ":" <key>`; parse at the first colon; the
  canonical form contains exactly one colon.
- **Grammar:** the ABNF above, plus the whole-key `.`/`..` exclusion as a
  named prose rule (URL dot-segment hazard; whole key only — interior `..` is
  opaque data).
- **Separator ownership:** placement rules exist only where the spec assigns a
  character meaning — `|` in keys, `-` in types. All other characters inside a
  segment (`.`, `_`, `@`, `-`) are opaque data from foreign grammars (emails,
  domains, versions) and carry no placement rules.
- **Case sensitivity:** identities are byte-wise case-sensitive; the type
  grammar is lowercase-only, key segments preserve case (ULIDs are uppercase).
- **Key opacity:** segments are non-empty by grammar; no implementation parses
  or interprets segment content or count.
- **Closed rejection-reason set:** `empty-type`, `empty-key`, `invalid-type`,
  `invalid-key`, `missing-separator`. Length violations and malformed segments
  classify as `invalid-type`/`invalid-key`; messages carry detail, callers
  classify on the constant.
- **Frozen-grammar rule:** loosening is permitted, tightening is not; persisted
  references must decode forever. The freeze begins at spec v1 (this revision
  lands before it). Changes bump the vector-file version.
- **Stores-adapt rule:** stores carry the canonical form verbatim where the
  transport allows, otherwise apply a deterministic, lossless, store-local
  encoding (e.g. JetStream `.` → `%2E` for key dots). A store never rejects,
  truncates, or constrains a valid identity.
- **URL carriage note:** in strict URL contexts only `|` percent-encodes
  (`%7C`); HTTP edges decode path parameters before parsing.
- **Conformance:** what an implementation must assert against the vector file
  (every vector, plus byte-for-byte re-encode round-trip for valid parse
  vectors), and an implementation-status table: Go conformant; Rust pending
  (item D); TypeScript pending migration from the dot form.

## 2. Vector file schema

Two groups, matching the two validation boundaries every implementation exposes
(construct from parts; parse an encoded string):

```json
{
  "spec": "aggregate-identity",
  "version": 1,
  "construct": [
    {"type": "counter", "key": "live-1", "valid": true},
    {"type": "Counter", "key": "live-1", "valid": false, "reason": "invalid-type"}
  ],
  "parse": [
    {"input": "gift-card:kevin|card|boots", "valid": true, "type": "gift-card", "key": "kevin|card|boots"},
    {"input": "counter", "valid": false, "reason": "missing-separator"},
    {"input": "counter:a||b", "valid": false, "reason": "invalid-key"}
  ]
}
```

Coverage requirements:

- Every rejection reason at both boundaries where applicable
  (`missing-separator` is parse-only).
- Type edges: uppercase rejected; leading digit rejected; leading, trailing,
  and doubled `-` rejected; digit-leading interior token valid (`base-64`);
  `.`, `_`, `~`, `|` rejected; 64-octet boundary (64 valid, 65 invalid).
- Key edges: `@` and `.` valid in segments; `~`, `%`, `:`, `/`, whitespace,
  non-ASCII rejected; leading/trailing/doubled `|` rejected; whole key of `.`
  or `..` rejected, while `a|..|b` and `v1..2` are valid (whole-key rule
  only); 512-octet boundary; email-form key (`user:kevin@example.com`);
  ULID-form key (uppercase preserved).
- Valid parse vectors round-trip: re-encoding the parsed parts reproduces the
  input bytes exactly.

Vectors pin boundary cases and cross-language agreement; the rapid generators
(`we/identity-gen.go`, ADR-0009), regenerated for the revised grammar, remain
Go's generative layer above them.

## 3. Versioning and vendoring

The `version` integer is the entire mechanism. Rust and TypeScript vendor a
verbatim copy of the vector file into their test trees; each consumer's
conformance test asserts `version == <expected>`, so a stale copy fails visibly
when the master moves. Updating a consumer = copy the new file, bump the
expectation, fix what the new vectors catch. No checksums, submodules, or
network fetches — the grammar freezes loosen-only at v1, so version bumps are
rare and deliberate.

## 4. Go implementation changes

- `we/aggregate-id.go`: `validIdentityPart` splits into type and key rules
  (type: lowercase kebab, letter-first, ≤ 64; key: segment grammar, ≤ 512,
  whole-key `.`/`..` exclusion). The reason set is unchanged.
- `we/identity-gen.go`: generators regenerated for the revised grammar
  (type `[a-z][a-z0-9-]*` letter-first; key as pipe-joined segments).
- `we/identity-vectors_test.go` (new): loads
  `../documents/spec/aggregate-identity.vectors.json` and asserts, for every
  vector, `MakeAggregateId` outcomes (construct group),
  `EncodedAggregateId.Decode` outcomes (parse group), the specific
  `InvalidAggregateIdError.Reason`, and the round-trip rule. Failures name the
  offending vector input.
- Existing identity tests and any literals using now-invalid spellings
  (uppercase types, `~`) are updated to the revised grammar.
- Store conformance suites re-run unchanged — the stores-adapt contract is
  unaffected; the JetStream `%2E` encoding now applies to key dots only.

Previously persisted development identities outside the revised grammar are
orphaned, not migrated (owner precedent, ADR-0008/IDENTITY-S4.R4: correctness
over backward compatibility; the port is unreleased).

## 5. Documentation sweep (drift retirement)

| Location | Change |
|---|---|
| `documents/adr/0010-identity-grammar.md` | New ADR: restates what survives from ADR-0008 (canonical form, one parser, stores-adapt, frozen-grammar, durable-reference contract), records the v2 grammar and its rationale, names the spec as the normative grammar |
| `documents/adr/0008-aggregate-identity.md` | Deleted; tombstone row in `documents/adr/README.md` (house supersession rule) |
| References to ADR-0008 | Repointed to ADR-0010 (roadmap, feature 07, code comments) |
| `documents/features/07-aggregate-identity.md` | IDENTITY-S1.R4/R7/R8 updated to reference the spec instead of restating the old charset |
| `we/aggregate-id.go`, `we/identity-gen.go` | Comments cite the spec instead of restating the grammar |
| `documents/conventions.md` | One-line pointer to the writing standard |

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
lookup rather than narrative brevity.

## Out of scope

- Rust and TypeScript code changes (roadmap item D and the future TypeScript
  migration consume this spec; they do not ship with it).
- Migration of previously persisted development data (orphaned, per precedent).
- Promotion to a separate spec repository — deferred until a second
  cross-implementation contract exists; the vendoring mechanism is unchanged by
  any future move.

## Testing

- The Go vector test is the acceptance gate for spec/vector agreement; the
  revised validator is the reference implementation of the grammar.
- TDD per house rules: vectors and failing tests precede the validator change.
- Full gates: lint 0; full uncached suite, 0 skips (Docker for store suites);
  restate integration.
