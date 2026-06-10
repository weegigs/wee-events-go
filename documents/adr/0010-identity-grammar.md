# ADR-0010 — Aggregate identity grammar v2: kebab types, segmented keys, shared normative spec

- **Status:** Accepted (supersedes ADR-0008)
- **Relates to:** [features/07-aggregate-identity.md](../features/07-aggregate-identity.md) · [spec/aggregate-identity.md](../spec/aggregate-identity.md)

## Context

ADR-0008 established the canonical `type:key` form, the validating
constructor with a closed reason set, the stores-adapt rule, and the
frozen-grammar (loosen-only) rule, with both parts drawn from RFC 3986
unreserved characters (keys additionally `|`). Formalising that grammar as a
document shared by all three implementations (Go, Rust, TypeScript) surfaced
charset weaknesses better fixed before the freeze: `~` is obscure to read
and was present only via the unreserved set; mixed-case types are
case-confusable schema names (`Counter` vs `counter` silently distinct — the
same hazard family as the dot-form silent merge ADR-0008 eliminated); email
keys — among the most common natural business keys — were inexpressible
without encoding; nothing bounded length, so the forever-frozen contract
admitted pathological keys every future store must carry; and the pipe
convention was prose, not grammar. The port is unreleased: this is the last
safe moment to tighten.

## Decision

1. **What survives from ADR-0008 unchanged:** the canonical
   `<type> ":" <key>` form parsed at the first colon; one boundary parser
   (`EncodedAggregateId.Decode` / `MakeAggregateId`); the closed
   rejection-reason set `{empty-type, empty-key, invalid-type, invalid-key,
   missing-separator}`; stores adapt to the key space, never the reverse,
   with deterministic lossless store-local encoding where a transport
   requires it; the encoded form is the durable-reference format; the
   grammar freezes loosen-only; correctness over backward compatibility —
   previously written development data is orphaned, not migrated.
2. **Grammar v2**, normatively defined in
   [`documents/spec/aggregate-identity.md`](../spec/aggregate-identity.md)
   (the spec is the single normative statement; this ADR records the
   decision and rationale): types are lowercase kebab — tokens of `[a-z0-9]`
   joined by single hyphens, letter-first, ≤ 64 octets. Keys are segments of
   `[A-Za-z0-9._@-]` joined by single pipes, ≤ 512 octets, the whole key
   never `.` or `..`.
3. **Separator ownership:** placement rules exist only where the spec
   assigns a character meaning — `|` in keys, `-` in types. All other
   admitted characters (`.`, `_`, `@`, `-` in segments) are opaque data from
   foreign grammars (emails, domains, versions) and carry no placement
   rules; the framework never interprets them.
4. **Shared conformance:** the spec ships with a machine-readable vector
   file. Go consumes it in-tree; Rust and TypeScript vendor verbatim copies
   whose pinned `version` fails visibly when stale. All three
   implementations bind to one identifier space.
5. **Internationalisation deferred:** keys stay ASCII. Unicode
   normalization (NFC/NFD) would let visually identical keys address
   different aggregates — a silent split. The recorded loosening path is
   non-ASCII letters/digits with NFC mandated and unnormalised input
   rejected, never silently normalised. Natural-language data belongs in
   payloads, which are full UTF-8 already.

## Consequences

- `Counter`, `a~b`, and over-long parts now fail validation; identities
  written by the v1 grammar outside v2 are orphaned development data.
- Emails work as keys verbatim (`user:kevin@example.com`); `@` is legal raw
  in URL path segments (RFC 3986 `pchar`), so only `|` percent-encodes in
  strict URL contexts, as before.
- The whole-key dot rule is deliberately narrow: `a|..|b` is legal opaque
  data — the URL dot-segment hazard exists only when the entire path
  component is a dot-segment.
- Worst-case canonical form is 577 octets — inside every surveyed transport
  bound (DynamoDB 1,024-octet sort key, ~2,000-character URL interop,
  NATS 4K control line) with multiples to spare, so the stores-adapt escape
  hatch is never exercised for length.
- Rust (`wee-events.rs`) currently accepts colon-bearing keys and any
  charset and documents that affordance; aligning it is roadmap item D, and
  the vector file is the contract it implements against.

## Alternatives considered

- **Keep RFC 3986 unreserved verbatim (ADR-0008).** Rejected: carries `~`
  nobody needs, mixed-case types nobody should write, and no email keys.
  "Unreserved" was the justification, not the requirement — the requirement
  is legible, losslessly encodable, unambiguous.
- **Per-segment `.`/`..` exclusion.** Initially drafted, rejected in owner
  review: the dot-segment hazard is whole-path-component only, and policing
  segment interiors parses opaque data — over-tightening with no hazard
  behind it.
- **Colons in keys (Rust's URN-style affordance).** Rejected: re-opens the
  exactly-one-colon legibility property; `run|01ABC` expresses the same
  composite within the grammar.
- **`+` and remaining sub-delims in keys.** Rejected: `+` decodes
  ambiguously (space in many decoders); quotes/parens/`*`/`;`/`=` read as
  prose or pattern syntax, not identity.
- **Unbounded length.** Rejected: a frozen grammar without caps obliges
  every future store to carry pathological keys forever; 64/512 costs no
  legitimate key.
