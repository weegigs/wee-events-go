# Aggregate Identity — Normative Specification

**Version:** 1 · **Vectors:** [`aggregate-identity.vectors.json`](aggregate-identity.vectors.json)
**Decision record:** [ADR-0010](../adr/0010-identity-grammar.md) (rationale lives there, not here)

This document is the single normative definition of the aggregate identity
grammar for every wee-events implementation (Go `wee-events-go`, Rust
`wee-events.rs`, TypeScript `wee-events`). Implementations conform to this
document and its vector file; the grammar never adapts to an implementation
or a store.

## Canonical form

An aggregate identity is two parts, `type` and `key`, with one canonical
string spelling:

```
<type> ":" <key>
```

Parsing splits at the **first** colon. Under the grammar below the canonical
form contains exactly one colon. The struct/JSON object form
(`{"type": …, "key": …}`) is out of scope; this document governs the parts
and the string spelling.

## Grammar

```abnf
identity = type ":" key
type     = word *( "-" token )              ; 1–64 octets
word     = lower *( lower / digit )
token    = 1*( lower / digit )
key      = segment *( "|" segment )         ; 1–512 octets
segment  = 1*( ALPHA / DIGIT / "-" / "." / "_" / "@" )
lower    = %x61-7A                          ; a-z
digit    = %x30-39                          ; 0-9
```

One rule the ABNF cannot express cleanly:

- **Whole-key dot rule:** the key as a whole is never `.` or `..`. The rule
  applies to the whole key only; `..` inside a key (`a|..|b`, `v1..2`) is
  legal.

Properties that follow from the grammar:

- Both parts are non-empty, pure ASCII, and contain no `:`, `%`, `/`,
  whitespace, or pattern metacharacters.
- Types contain no separator at the edges: no leading digit or hyphen, no
  trailing or doubled hyphen.
- Keys contain no leading, trailing, or doubled `|`; every segment carries
  at least one character.
- Identities are byte-wise case-sensitive. The type grammar is
  lowercase-only; key segments preserve case.
- Length caps are octet counts of the parts (the canonical form is therefore
  at most 577 octets).

## Separator ownership

Placement rules exist only where this specification assigns a character
meaning: `|` in keys (composite-segment separator) and `-` in types
(kebab-case token separator). Every other character inside a segment —
`.`, `_`, `@`, `-` — is opaque data admitted for foreign grammars (emails,
domains, versions) and carries no placement rules.

## Key opacity

Keys are semantically opaque. No implementation parses, validates, or
interprets segment content or count. `|` is the documented convention for
composite keys (`kevin|card|boots`); the grammar guarantees well-formedness,
nothing more.

## Rejection reasons

Validation failures classify into a closed set. Implementations map native
error types onto these identifiers; messages may carry detail, callers
classify on the identifier.

| Reason | Meaning |
|---|---|
| `empty-type` | type is empty |
| `empty-key` | key is empty |
| `invalid-type` | type violates the grammar (charset, shape, or length) |
| `invalid-key` | key violates the grammar (charset, shape, length, or whole-key dot rule) |
| `missing-separator` | encoded form contains no `:` |

`missing-separator` applies only when parsing the encoded form. Emptiness is
reported with its own reason, never as `invalid-*`.

## Frozen grammar

The grammar freezes at version 1: **loosening is permitted, tightening is
not** — persisted references must decode forever. Any change bumps the
vector-file version.

Recorded loosening path (deferred, not normative): non-ASCII letters/digits
in key segments, NFC-mandated, with unnormalised input rejected — never
silently normalised. See ADR-0010.

## Stores adapt

Stores derive storage keys from the canonical form. Where a transport cannot
carry it verbatim, the store applies a deterministic, lossless, store-local
encoding, invisible to callers and reversed on read (e.g. NATS JetStream
encodes key dots as `%2E` in subjects). A store never rejects, truncates, or
constrains a valid identity. In strict URL contexts only `|` percent-encodes
(`%7C`); HTTP edges decode path parameters before parsing.

## Conformance

An implementation is conformant when, against the current vector file:

1. Every `construct` vector produces the expected outcome from the
   implementation's validating constructor — acceptance with the exact
   parts, or rejection with the exact reason.
2. Every `parse` vector produces the expected outcome from the
   implementation's canonical-form parser.
3. Every valid `parse` vector round-trips: re-encoding the parsed parts
   reproduces the input byte-for-byte.
4. The conformance test asserts the vector file `version` it was written
   against, so a stale vendored copy fails visibly.

Consumption: this repository reads the file in-tree; other implementations
vendor a verbatim copy into their test trees.

| Implementation | Status |
|---|---|
| Go (`wee-events-go`) | Conformant (in-tree) |
| Rust (`wee-events.rs`) | Conformant (vendored vectors, spec v1) |
| TypeScript (`wee-events`) | Pending — legacy `type.key` dot form; migration unscheduled |
