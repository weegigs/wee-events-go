# Identity Grammar v2 + Outstanding Follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking. Workers NEVER run jj/git — the coordinator serialises every `jj split` after review.

**Goal:** Formalise the aggregate identity grammar (v2) as a shared normative spec with conformance vectors consumed by Go and Rust, then clear the remaining roadmap follow-ups (envelope opacity, Kurrent reconnect investigation, Restate harness check).

**Architecture:** One normative spec + JSON vector file in `documents/spec/` (this repo is the ecosystem-document home). Go implements grammar v2 and consumes the vectors in-tree; Rust vendors the vectors. ADR-0010 supersedes ADR-0008. Item A retypes `we.Data.Data` to `[]byte` so JSON-envelope stores carry any payload encoding. Item B ends at an investigation checkpoint; item E records still-blocked evidence.

**Tech Stack:** Go 1.26 (mise), `pgregory.net/rapid`, testify, jj (split-based commits, coordinator-only), Rust (wee-events.rs sibling repo), CodeRabbit per phase.

**Spec:** `docs/superpowers/specs/2026-06-10-identity-charset-design.md` (approved). Read it before starting any task.

**Sequencing:** Phase 1 (Tasks 1–8) → Phase 2 (Tasks 9–11, Rust, consumes Phase 1's vectors) → Phase 3 (Task 12) → Phase 4 (Tasks 13–16, independent of 1–3) → Phase 5 (Task 17, independent). Phases 3/4/5 may run any time after Phase 1; Phase 2 requires Phase 1 complete.

**Conventions binding every task:** `mise exec --` wraps every Go command. Commits are `jj split <fileset> -m "<past tense>"` by the coordinator only, one commit per task, exact fileset listed in the task. No lint suppressions. Tests assert specific error variants via `errors.As`. After any suite run, check `$?` directly and `jj st` for stray `testdata/rapid` failfiles — delete them before committing.

---

## Phase 1 — Item C: grammar v2, spec, vectors, Go implementation

### Task 1: Writing standard document

**Files:**
- Create: `documents/writing-documents.md`
- Modify: `documents/conventions.md` (append section at end)

- [x] **Step 1: Write `documents/writing-documents.md`** with exactly this content:

````markdown
# Writing Documents

Documents produced for this ecosystem follow one rule: **do not ship the AI
draft**. A draft is raw material; the edit — selection, compression,
judgment — is what makes it a document. The failure mode is *workslop*:
polished-looking output that decides nothing and dumps the thinking on the
reader.

The test that runs through everything below: **could the document be cut from
the bottom and keep its impact?** If not, restructure it.

## Order: cut from the bottom

1. Point
2. Decision or recommendation
3. Reasoning
4. Trade-offs
5. Operational detail, edge cases, appendix

The reader who stops early loses depth, not meaning. The conclusion is never
a reward for finishing. Open with the answer — the conclusion, the decision,
the request — then explain why.

## Keep what cannot be regenerated

The reader has the same tools the author has. Definitions, background, and
obvious objections can be rebuilt on demand — leave them out. Spend the
document on what cannot be rebuilt: the numbers, the context, what was
decided and why. Compression beats coverage: the question is never "is this
comprehensive?" but "can the right reader understand, decide, or act with
minimal friction?"

## Voice

- Objective third person, no personal pronouns (house rule; overrides any
  example elsewhere written as "we should…").
- No throat-clearing: "It is important to note that…", "This document aims
  to…", "In conclusion…" — replace each with a direct claim.
- Specific over smooth: concrete nouns, verbs, numbers, owners. "Cut
  onboarding from seven steps to four", not "improve the onboarding journey".
- Headings carry the argument: reading the headings alone should still
  follow it.

## Length budgets

| Job | Target |
|---|---|
| Decision ask | 1 page |
| Technical design | 3–6 pages + appendix |
| Progress update | 5–10 bullets |
| Summary | 150–300 words |

Longer is allowed; the burden of proof rises with length.

## Exception: normative reference documents

Specs, grammars, and conformance documents optimise for **precision and
lookup**, not narrative brevity. In a normative document an unstated case is
undefined behaviour — completeness is the point. The voice and
regenerable-content rules still apply; the cut-from-the-bottom rule does not.

## Pre-publish checklist

1. Is the point visible in the first 30 seconds?
2. Does the opening start with the answer?
3. Could it be 30 percent shorter?
4. Do the headings mean something on their own?
5. Are generic claims replaced with specific facts?
6. Has detail moved down or out?
````

- [x] **Step 2: Append to `documents/conventions.md`** (at end of file):

```markdown

## Writing documents

Documents follow [`writing-documents.md`](writing-documents.md): decision
first, cut-from-the-bottom structure, compression over coverage, objective
third-person voice. Normative reference documents (specs, grammars) optimise
for precision and lookup instead — see the exception there.
```

- [x] **Step 3: Coordinator commits**

```bash
jj split documents/writing-documents.md documents/conventions.md -m "Added the document-writing standard and linked it from conventions"
```

### Task 2: Normative spec document

**Files:**
- Create: `documents/spec/aggregate-identity.md`

- [x] **Step 1: Write `documents/spec/aggregate-identity.md`** with exactly this content:

````markdown
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
| Rust (`wee-events.rs`) | Pending — roadmap item D |
| TypeScript (`wee-events`) | Pending — legacy `type.key` dot form; migration unscheduled |
````

- [x] **Step 2: Coordinator commits**

```bash
jj split documents/spec/aggregate-identity.md -m "Added the normative aggregate-identity grammar specification (spec v1)"
```

### Task 3: Conformance vector file

**Files:**
- Create: `documents/spec/aggregate-identity.vectors.json`

- [x] **Step 1: Write `documents/spec/aggregate-identity.vectors.json`**:

```json
{
  "spec": "aggregate-identity",
  "version": 1,
  "construct": [
    {"type": "counter", "key": "live-1", "valid": true},
    {"type": "order-line-v2", "key": "01HX-abc_2026.06.10@final", "valid": true},
    {"type": "inventory", "key": "kevin|card|boots", "valid": true},
    {"type": "user", "key": "kevin@example.com", "valid": true},
    {"type": "base-64", "key": "A", "valid": true},
    {"type": "a", "key": "0", "valid": true},
    {"type": "run", "key": ".well-known", "valid": true},
    {"type": "run", "key": "a|..|b", "valid": true},
    {"type": "run", "key": "v1..2", "valid": true},
    {"type": "", "key": "k", "valid": false, "reason": "empty-type"},
    {"type": "counter", "key": "", "valid": false, "reason": "empty-key"},
    {"type": "Counter", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "a~b", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "2fa", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "-a", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "a-", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "a--b", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "a_b", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "a.b", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "a@b", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "a|b", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "counter:evil", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "cust/omer", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "Ω", "key": "k", "valid": false, "reason": "invalid-type"},
    {"type": "counter", "key": "k~v", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "tenant:42", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "has space", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "a%20b", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "a*b", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "a/b", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": ".", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "..", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "|k", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "k|", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "a||b", "valid": false, "reason": "invalid-key"},
    {"type": "counter", "key": "café", "valid": false, "reason": "invalid-key"}
  ],
  "parse": [
    {"input": "counter:live-1", "valid": true, "type": "counter", "key": "live-1"},
    {"input": "gift-card:kevin|card|boots", "valid": true, "type": "gift-card", "key": "kevin|card|boots"},
    {"input": "user:kevin@example.com", "valid": true, "type": "user", "key": "kevin@example.com"},
    {"input": "run:a|..|b", "valid": true, "type": "run", "key": "a|..|b"},
    {"input": "run:v1..2", "valid": true, "type": "run", "key": "v1..2"},
    {"input": "counter", "valid": false, "reason": "missing-separator"},
    {"input": "", "valid": false, "reason": "missing-separator"},
    {"input": ":key", "valid": false, "reason": "empty-type"},
    {"input": "type:", "valid": false, "reason": "empty-key"},
    {"input": "Counter:k", "valid": false, "reason": "invalid-type"},
    {"input": "counter:a:b", "valid": false, "reason": "invalid-key"},
    {"input": "counter:k~v", "valid": false, "reason": "invalid-key"},
    {"input": "counter:a||b", "valid": false, "reason": "invalid-key"},
    {"input": "counter:..", "valid": false, "reason": "invalid-key"},
    {"input": "counter: k", "valid": false, "reason": "invalid-key"}
  ]
}
```

- [x] **Step 2: Append the four length-boundary vectors** (too long to hand-author safely):

```bash
python3 - <<'EOF'
import json
p = 'documents/spec/aggregate-identity.vectors.json'
v = json.load(open(p))
v['construct'] += [
    {"type": "a" * 64, "key": "k", "valid": True},
    {"type": "a" * 65, "key": "k", "valid": False, "reason": "invalid-type"},
    {"type": "t", "key": "k" * 512, "valid": True},
    {"type": "t", "key": "k" * 513, "valid": False, "reason": "invalid-key"},
]
with open(p, 'w') as f:
    json.dump(v, f, indent=2, ensure_ascii=False)
    f.write('\n')
EOF
```

- [x] **Step 3: Verify the file** — valid JSON, boundary lengths exact:

```bash
jq -r '[.construct[].type | length] | max' documents/spec/aggregate-identity.vectors.json
```
Expected: `65`

```bash
jq -r '[.construct[].key | length] | max' documents/spec/aggregate-identity.vectors.json
```
Expected: `513`

- [x] **Step 4: Coordinator commits**

```bash
jj split documents/spec/aggregate-identity.vectors.json -m "Added the aggregate-identity conformance vector file (spec v1, 55 vectors)"
```

### Task 4: Grammar v2 in Go — tests, validator, generators

The validator, generators, and their tests are inseparable for a green tree
(the round-trip property feeds generator output into the validator), so this
is one task and one commit.

**Files:**
- Modify: `we/aggregate-id.go` (full replacement below)
- Modify: `we/aggregate-id_test.go` (full replacement below)
- Modify: `we/identity-gen.go` (full replacement below)

- [x] **Step 1: Replace `we/aggregate-id_test.go`** with:

```go
package we

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// IDENTITY-S1 — the validating constructor and its closed reason set
// (normative grammar: documents/spec/aggregate-identity.md).
func TestMakeAggregateId(t *testing.T) {
	t.Run("valid identity round-trips the parts", func(t *testing.T) {
		id, err := MakeAggregateId("customer", "0042")
		require.NoError(t, err)
		assert.Equal(t, AggregateId{Type: "customer", Key: "0042"}, id)
	})

	t.Run("the full grammar is accepted", func(t *testing.T) {
		id, err := MakeAggregateId("order-line-v2", "01HX-abc_2026.06.10@final")
		require.NoError(t, err)
		assert.Equal(t, "01HX-abc_2026.06.10@final", id.Key)
	})

	t.Run("composite keys use the pipe convention", func(t *testing.T) {
		id, err := MakeAggregateId("inventory", "kevin|card|boots")
		require.NoError(t, err)
		assert.Equal(t, "kevin|card|boots", id.Key, "the key is opaque — segments are never parsed")
	})

	t.Run("length caps sit at 64 and 512 octets", func(t *testing.T) {
		_, err := MakeAggregateId("a"+strings.Repeat("b", 63), strings.Repeat("k", 512))
		require.NoError(t, err)
	})

	t.Run("interior dot segments are opaque data", func(t *testing.T) {
		for _, key := range []string{"a|..|b", "v1..2", ".well-known"} {
			_, err := MakeAggregateId("run", key)
			require.NoError(t, err, "key %q", key)
		}
	})

	cases := []struct {
		name, typ, key, reason string
	}{
		{"empty type", "", "k", ReasonEmptyType},
		{"empty key", "customer", "", ReasonEmptyKey},
		{"uppercase type", "Customer", "k", ReasonInvalidType},
		{"tilde in type", "a~b", "k", ReasonInvalidType},
		{"leading digit type", "2fa", "k", ReasonInvalidType},
		{"leading hyphen type", "-a", "k", ReasonInvalidType},
		{"trailing hyphen type", "a-", "k", ReasonInvalidType},
		{"doubled hyphen type", "a--b", "k", ReasonInvalidType},
		{"underscore in type", "a_b", "k", ReasonInvalidType},
		{"dot in type", "a.b", "k", ReasonInvalidType},
		{"at in type", "a@b", "k", ReasonInvalidType},
		{"pipe in type", "a|b", "k", ReasonInvalidType},
		{"colon in type", "customer:evil", "k", ReasonInvalidType},
		{"slash in type", "cust/omer", "k", ReasonInvalidType},
		{"over-long type", "a" + strings.Repeat("b", 64), "k", ReasonInvalidType},
		{"tilde in key", "customer", "k~v", ReasonInvalidKey},
		{"colon in key", "customer", "tenant:42", ReasonInvalidKey},
		{"space in key", "customer", "has space", ReasonInvalidKey},
		{"percent in key", "customer", "a%20b", ReasonInvalidKey},
		{"nats wildcard in key", "customer", "a*b", ReasonInvalidKey},
		{"whole-key single dot", "customer", ".", ReasonInvalidKey},
		{"whole-key dot segment", "customer", "..", ReasonInvalidKey},
		{"leading pipe key", "customer", "|k", ReasonInvalidKey},
		{"trailing pipe key", "customer", "k|", ReasonInvalidKey},
		{"doubled pipe key", "customer", "a||b", ReasonInvalidKey},
		{"non-ascii key", "customer", "café", ReasonInvalidKey},
		{"over-long key", "customer", strings.Repeat("k", 513), ReasonInvalidKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := MakeAggregateId(tc.typ, tc.key)
			require.Error(t, err)
			assert.Equal(t, AggregateId{}, id)
			var invalid *InvalidAggregateIdError
			require.True(t, errors.As(err, &invalid), "expected *InvalidAggregateIdError, got %T", err)
			assert.Equal(t, tc.reason, invalid.Reason)
			assert.Equal(t, tc.typ, invalid.Type)
			assert.Equal(t, tc.key, invalid.Key)
		})
	}
}

// IDENTITY-S2.R1 / IDENTITY-S3 — canonical form matches Rust Display; Decode
// is the exact inverse with the closed parse errors.
func TestCanonicalEncoding(t *testing.T) {
	t.Run("encodes type:key matching Rust Display", func(t *testing.T) {
		id := AggregateId{Type: "counter", Key: "live-1"}
		assert.Equal(t, EncodedAggregateId("counter:live-1"), id.Encode())
	})

	t.Run("round-trips identities across the key grammar", func(t *testing.T) {
		for _, key := range []string{"0042", "2026.06.10-17", "01HX_abc", "a-b.c_d@e", "kevin|card|boots", "a|..|b"} {
			id, err := MakeAggregateId("order", key)
			require.NoError(t, err)
			decoded, err := id.Encode().Decode()
			require.NoError(t, err)
			assert.Equal(t, id, decoded, "key %q", key)
		}
	})

	// IDENTITY-S3.R4 (ADR-0009) — generative round-trip over the full grammar,
	// with shrinking to minimal counterexamples.
	t.Run("property: every constructible identity round-trips", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			typ := IdentityTypeGen().Draw(rt, "type")
			key := IdentityKeyGen().Draw(rt, "key")
			id, err := MakeAggregateId(typ, key)
			require.NoError(rt, err)
			decoded, err := id.Encode().Decode()
			require.NoError(rt, err)
			require.Equal(rt, id, decoded)
		})
	})

	// IDENTITY-S3.R4 companion — injecting any out-of-charset rune into either
	// part is rejected with the correct closed-set reason. Placement rules for
	// in-charset runes are pinned by the vector file, not this property.
	t.Run("property: out-of-charset characters are rejected", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			typ := IdentityTypeGen().Draw(rt, "type")
			key := IdentityKeyGen().Draw(rt, "key")

			part := rapid.SampledFrom([]string{"type", "key"}).Draw(rt, "part")
			target, charset, reason := key, identityKeyRunes, ReasonInvalidKey
			if part == "type" {
				target, charset, reason = typ, identityTypeRunes, ReasonInvalidType
			}

			bad := rapid.Rune().Filter(func(r rune) bool {
				return !strings.ContainsRune(charset, r)
			}).Draw(rt, "bad")
			pos := rapid.IntRange(0, len(target)).Draw(rt, "pos") // generator output is ASCII; byte positions are rune positions
			mutated := target[:pos] + string(bad) + target[pos:]

			if part == "type" {
				typ = mutated
			} else {
				key = mutated
			}

			_, err := MakeAggregateId(typ, key)
			require.Error(rt, err)
			var invalid *InvalidAggregateIdError
			require.True(rt, errors.As(err, &invalid), "got %T", err)
			require.Equal(rt, reason, invalid.Reason)
		})
	})

	decodeFailures := []struct {
		name, input, reason string
	}{
		{"missing separator", "no-colon-here", ReasonMissingSeparator},
		{"empty type", ":key", ReasonEmptyType},
		{"empty key", "type:", ReasonEmptyKey},
		{"empty string", "", ReasonMissingSeparator},
		{"second colon lands in the key", "type:a:b", ReasonInvalidKey},
		{"space in key", "type:a b", ReasonInvalidKey},
		{"uppercase type", "Counter:k", ReasonInvalidType},
	}
	for _, tc := range decodeFailures {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EncodedAggregateId(tc.input).Decode()
			require.Error(t, err)
			var invalid *InvalidAggregateIdError
			require.True(t, errors.As(err, &invalid))
			assert.Equal(t, tc.reason, invalid.Reason)
		})
	}
}
```

- [x] **Step 2: Replace `we/identity-gen.go`** with:

```go
package we

import "pgregory.net/rapid"

// IdentityTypeGen generates aggregate types across the type grammar —
// kebab-case tokens, letter-first (documents/spec/aggregate-identity.md,
// ADR-0009). Bounded well inside the 64-octet cap; the cap boundary is
// pinned by the conformance vectors, not the generator.
func IdentityTypeGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-z][a-z0-9]{0,9}(-[a-z0-9]{1,10}){0,3}`)
}

// IdentityKeyGen generates aggregate keys across the key grammar, including
// pipe-joined composite segments (documents/spec/aggregate-identity.md,
// ADR-0009). Bounded well inside the 512-octet cap; the whole-key dot rule
// is the only filter the grammar shape cannot express.
func IdentityKeyGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9._@-]{1,16}(\|[A-Za-z0-9._@-]{1,16}){0,5}`).
		Filter(func(s string) bool { return s != "." && s != ".." })
}
```

- [x] **Step 3: Run the package tests to verify the new tests fail against the old validator**

```bash
mise exec -- go test -count=1 ./we/ -run 'TestMakeAggregateId|TestCanonicalEncoding'
```
Expected: FAIL — at minimum "uppercase type", "tilde in type", "trailing hyphen type", "over-long key" reject nothing under the v1 validator.

- [x] **Step 4: Replace `we/aggregate-id.go`** with:

```go
package we

import (
	"fmt"
)

// Reasons an aggregate identity is rejected. The set is closed so callers
// classify on the constant, never on message text
// (documents/spec/aggregate-identity.md §Rejection reasons).
const (
	ReasonEmptyType        = "empty-type"
	ReasonEmptyKey         = "empty-key"
	ReasonInvalidType      = "invalid-type"
	ReasonInvalidKey       = "invalid-key"
	ReasonMissingSeparator = "missing-separator"
)

// InvalidAggregateIdError reports a rejected aggregate identity, carrying the
// offending parts and the closed-set Reason.
type InvalidAggregateIdError struct {
	Type   string
	Key    string
	Reason string
}

func (e *InvalidAggregateIdError) Error() string {
	return fmt.Sprintf("invalid aggregate id (type %q, key %q): %s", e.Type, e.Key, e.Reason)
}

// Length caps in octets (documents/spec/aggregate-identity.md §Grammar).
const (
	maxIdentityTypeOctets = 64
	maxIdentityKeyOctets  = 512
)

// identityTypeRunes / identityKeyRunes list every rune the grammar admits in
// each part (documents/spec/aggregate-identity.md). Placement rules — token
// and segment shape, length caps, the whole-key dot rule — live in the
// validators below; the spec is normative, these constants are not.
const (
	identityTypeRunes = "abcdefghijklmnopqrstuvwxyz0123456789-"
	identityKeyRunes  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._@|"
)

// validIdentityType implements the type grammar: kebab-case tokens of
// [a-z0-9] joined by single hyphens, first token starting with a letter,
// at most 64 octets. Byte iteration is exact — any non-ASCII byte falls to
// the default arm.
func validIdentityType(s string) bool {
	if s == "" || len(s) > maxIdentityTypeOctets {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	previousHyphen := false
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case ('a' <= c && c <= 'z') || ('0' <= c && c <= '9'):
			previousHyphen = false
		case c == '-':
			if previousHyphen {
				return false
			}
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

// validIdentityKey implements the key grammar: segments of
// [A-Za-z0-9._@-] joined by single pipes, at most 512 octets, and the key
// as a whole is never "." or ".." (the URL dot-segment rule is whole-key
// only — interior dots are opaque data).
func validIdentityKey(s string) bool {
	if s == "" || len(s) > maxIdentityKeyOctets || s == "." || s == ".." {
		return false
	}
	previousBoundary := true // the start of the key opens a segment
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9'),
			c == '-', c == '.', c == '_', c == '@':
			previousBoundary = false
		case c == '|':
			if previousBoundary {
				return false
			}
			previousBoundary = true
		default:
			return false
		}
	}
	return !previousBoundary
}

// MakeAggregateId is the validating constructor for untrusted identity parts
// (IDENTITY-S1; normative grammar documents/spec/aggregate-identity.md).
// Emptiness is reported with its own reason; every other violation —
// charset, shape, or length — is invalid-type/invalid-key. Keys are
// semantically opaque: the grammar guarantees segment well-formedness,
// nothing here interprets segments.
func MakeAggregateId(aggregateType string, key string) (AggregateId, error) {
	switch {
	case aggregateType == "":
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonEmptyType}
	case key == "":
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonEmptyKey}
	case !validIdentityType(aggregateType):
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonInvalidType}
	case !validIdentityKey(key):
		return AggregateId{}, &InvalidAggregateIdError{Type: aggregateType, Key: key, Reason: ReasonInvalidKey}
	}
	return AggregateId{Type: aggregateType, Key: key}, nil
}
```

- [x] **Step 5: Run the package tests to verify green**

```bash
mise exec -- go test -count=1 ./we/
```
Expected: PASS, no skips. If a rapid failfile appears under `we/testdata/rapid/`, investigate before proceeding — then delete it.

- [x] **Step 6: Run the full unit suite** — other packages construct identities (samples, connectors, store unit tests); any literal now invalid under v2 fails here. Fix each failing literal to a v2-valid spelling (lowercase kebab type; no `~`).

```bash
mise exec -- go test -count=1 ./... 2>&1 | tail -20; echo "exit: $?"
```
Expected: `exit: 0` (Docker-dependent integration tests may require Docker running; if unavailable, run them in Task 8's gate instead and say so honestly in the report).

- [x] **Step 7: Coordinator commits** (include any literal fixes from Step 6 in the fileset, naming them)

```bash
jj split we/aggregate-id.go we/aggregate-id_test.go we/identity-gen.go -m "Tightened the identity grammar to v2: kebab types, pipe-segmented keys, '@' admitted, '~' dropped, 64/512 octet caps"
```

### Task 5: Go vector conformance test

**Files:**
- Create: `we/identity-vectors_test.go`

- [x] **Step 1: Write `we/identity-vectors_test.go`**:

```go
package we

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The conformance contract (documents/spec/aggregate-identity.md
// §Conformance): every vector asserted, valid parse vectors round-trip
// byte-for-byte, and the expected file version is pinned so a grammar bump
// fails visibly here.
const (
	identityVectorsPath    = "../documents/spec/aggregate-identity.vectors.json"
	identityVectorsVersion = 1
)

type identityVectorFile struct {
	Spec      string            `json:"spec"`
	Version   int               `json:"version"`
	Construct []constructVector `json:"construct"`
	Parse     []parseVector     `json:"parse"`
}

type constructVector struct {
	Type   string `json:"type"`
	Key    string `json:"key"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

type parseVector struct {
	Input  string `json:"input"`
	Valid  bool   `json:"valid"`
	Type   string `json:"type,omitempty"`
	Key    string `json:"key,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func loadIdentityVectors(t *testing.T) identityVectorFile {
	t.Helper()
	raw, err := os.ReadFile(identityVectorsPath)
	require.NoError(t, err)
	var file identityVectorFile
	require.NoError(t, json.Unmarshal(raw, &file))
	require.Equal(t, "aggregate-identity", file.Spec)
	require.Equal(t, identityVectorsVersion, file.Version, "vector file version moved — update this test against the new grammar")
	require.NotEmpty(t, file.Construct)
	require.NotEmpty(t, file.Parse)
	return file
}

func TestIdentityConstructVectors(t *testing.T) {
	for _, v := range loadIdentityVectors(t).Construct {
		t.Run(v.Type+":"+v.Key, func(t *testing.T) {
			id, err := MakeAggregateId(v.Type, v.Key)
			if v.Valid {
				require.NoError(t, err)
				assert.Equal(t, AggregateId{Type: v.Type, Key: v.Key}, id)
				return
			}
			require.Error(t, err)
			var invalid *InvalidAggregateIdError
			require.True(t, errors.As(err, &invalid), "got %T", err)
			assert.Equal(t, v.Reason, invalid.Reason)
		})
	}
}

func TestIdentityParseVectors(t *testing.T) {
	for _, v := range loadIdentityVectors(t).Parse {
		t.Run(v.Input, func(t *testing.T) {
			id, err := EncodedAggregateId(v.Input).Decode()
			if v.Valid {
				require.NoError(t, err)
				assert.Equal(t, AggregateId{Type: v.Type, Key: v.Key}, id)
				assert.Equal(t, v.Input, id.Encode().String(), "valid parse vectors round-trip byte-for-byte")
				return
			}
			require.Error(t, err)
			var invalid *InvalidAggregateIdError
			require.True(t, errors.As(err, &invalid), "got %T", err)
			assert.Equal(t, v.Reason, invalid.Reason)
		})
	}
}
```

- [x] **Step 2: Run it**

```bash
mise exec -- go test -count=1 ./we/ -run 'TestIdentityConstructVectors|TestIdentityParseVectors' -v 2>&1 | tail -15; echo "exit: $?"
```
Expected: `exit: 0`, 55 subtests passed. A failure means vector and validator disagree — determine which is wrong against the spec, fix that one, re-run.

- [x] **Step 3: Coordinator commits**

```bash
jj split we/identity-vectors_test.go -m "Bound the Go implementation to the shared conformance vectors (spec v1)"
```

### Task 6: ADR-0010 supersedes ADR-0008

**Files:**
- Create: `documents/adr/0010-identity-grammar.md`
- Delete: `documents/adr/0008-aggregate-identity.md`
- Modify: `documents/adr/README.md` (index)
- Modify: `documents/roadmap.md` (decisions table + ADR-0008 references)
- Modify: `we/event.go` (comment references)

- [x] **Step 1: Write `documents/adr/0010-identity-grammar.md`**:

````markdown
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
````

- [x] **Step 2: Delete the superseded ADR**

```bash
rm documents/adr/0008-aggregate-identity.md
```

- [x] **Step 3: Update `documents/adr/README.md`** index rows — replace the 0008 row and append 0010:

```markdown
| 0008 | Aggregate identity: canonical `type:key` form and validated construction | Superseded by 0010 — removed |
```
```markdown
| [0010](0010-identity-grammar.md) | Aggregate identity grammar v2: kebab types, segmented keys, shared normative spec | Accepted |
```

- [x] **Step 4: Repoint every ADR-0008 reference.** Find them:

```bash
grep -rn "0008-aggregate-identity\|ADR-0008" --include="*.go" --include="*.md" . | grep -v docs/superpowers | grep -v "adr/README"
```

Known sites and their treatment:
- `documents/roadmap.md` decisions table: 0008 row becomes `| 0008 | Aggregate identity: canonical `type:key` form and validated construction | Superseded by 0010 — removed |`; add the 0010 row. The item-D follow-up paragraph: replace `([ADR-0008](adr/0008-aggregate-identity.md))` with `([spec](spec/aggregate-identity.md), [ADR-0010](adr/0010-identity-grammar.md))` and replace its last sentence with: "Implement against the shared spec and vendor its conformance vectors (`documents/spec/aggregate-identity.vectors.json`)."
- `we/event.go:37–41` comment: replace `(see ADR-0008)` with `(documents/spec/aggregate-identity.md, ADR-0010)`.
- `we/event.go:44–47` Encode comment: replace `(IDENTITY-S2, ADR-0008)` with `(IDENTITY-S2, ADR-0010)`.
- `documents/features/07-aggregate-identity.md`: every `ADR-0008` link → `ADR-0010` (`adr/0010-identity-grammar.md`); handled fully in Task 7.
- Any remaining hits from the grep: repoint the same way; the design doc and this plan under `docs/superpowers/` stay as-is (historical records).

- [x] **Step 5: Verify no live references remain**

```bash
grep -rn "0008-aggregate-identity" --include="*.go" --include="*.md" . | grep -v docs/superpowers | grep -v "adr/README"
```
Expected: no output.

- [x] **Step 6: Coordinator commits**

```bash
jj split documents/adr/ documents/roadmap.md we/event.go documents/features/07-aggregate-identity.md -m "Superseded ADR-0008 with ADR-0010: grammar v2 decision, spec named normative, references repointed"
```
(If Task 7 runs in the same working copy first, coordinate filesets so this commit carries only the ADR/reference changes.)

### Task 7: Feature 07 and store-comment sweep

**Files:**
- Modify: `documents/features/07-aggregate-identity.md` (R4, R7, R8)
- Modify: `stores/jetstream/jetstream.go:84–89` (subject comment)
- Modify: `we/event-store-validation-suite.go` (`IdentityRoundTripsThroughStorage` godoc — still says "RFC 3986 unreserved runes")

- [x] **Step 1: Replace requirement IDENTITY-S1.R4** body with:

```markdown
- **IDENTITY-S1.R4** (ubiquitous) — The parts shall conform to the normative
  grammar in [`documents/spec/aggregate-identity.md`](../spec/aggregate-identity.md)
  (grammar v2, [ADR-0010](../adr/0010-identity-grammar.md)): types are
  lowercase kebab tokens, letter-first, ≤ 64 octets; keys are pipe-joined
  segments of `[A-Za-z0-9._@-]`, ≤ 512 octets, never `.` or `..` as a whole.
  The grammar is defined by identity-domain concerns alone — legibility,
  lossless encodability, non-ambiguity — never by any store's transport
  (stores adapt to the key space; IDENTITY-S4).
```

- [x] **Step 2: Replace requirement IDENTITY-S1.R7** body with:

```markdown
- **IDENTITY-S1.R7** (ubiquitous) — All implementations bind to the shared
  grammar through the conformance vector file
  (`documents/spec/aggregate-identity.vectors.json`); per-implementation
  status is tracked in the spec's conformance table. Until an implementation
  aligns, identities it writes outside the grammar fail this parser loudly —
  an error, never a transformation.
```

- [x] **Step 3: Update requirement IDENTITY-S1.R8**: replace the sentence "`"|"` is the documented convention for composite keys (e.g. `kevin|card|boots`); the framework shall treat the key as opaque and never parse, validate, or enforce segment structure." with:

```markdown
- **IDENTITY-S1.R8** (ubiquitous) — `"|"` is the composite-key segment
  separator, formalised in the grammar (segments non-empty, pipes interior
  only); the framework shall treat segment content and count as opaque and
  never parse or interpret them. In strict URL contexts `"|"`
  percent-encodes deterministically (`%7C`) and the HTTP edge decodes path
  parameters, so both spellings reach `MakeAggregateId` as the same identity.
```
(Keep the requirement's remaining text as-is if it already matches the second sentence.)

- [x] **Step 4: Update the jetstream subject comment** (`stores/jetstream/jetstream.go`, the paragraph ending at the `subject` function). Replace the sentence listing the remaining runes with:

```go
// occurs in the canonical form. Only keys carry '.' under grammar v2 (types
// are kebab); the remaining canonical runes (ALPHA, DIGIT, '-', '_', '@',
// '|', and the ':' joiner) are all legal NATS token characters
// (documents/spec/aggregate-identity.md).
```

- [x] **Step 4a: Update the suite scenario godoc** — in `we/event-store-validation-suite.go`, the `IdentityRoundTripsThroughStorage` comment, replace "across the full charset grammar of both identity parts — generated types over the RFC 3986 unreserved runes, generated keys additionally including the '|' composite separator —" with:

```go
// exact aggregate identity and payload for property-generated inputs across
// the full grammar of both identity parts (grammar v2,
// documents/spec/aggregate-identity.md — kebab types; pipe-segmented keys)
```

- [x] **Step 5: Verify the suite still passes** (comments only, but the feature doc tables reference test names):

```bash
mise exec -- go test -count=1 ./we/ ./stores/jetstream/ 2>&1 | tail -5; echo "exit: $?"
```
Expected: `exit: 0` (jetstream integration tests need Docker; if unavailable, unit tests must still pass and Task 8 covers the rest).

- [x] **Step 6: Coordinator commits**

```bash
jj split documents/features/07-aggregate-identity.md stores/jetstream/jetstream.go we/event-store-validation-suite.go -m "Pointed feature 07 requirements and the stale store/suite comments at the normative grammar spec"
```

### Task 8: Phase 1 gate

- [x] **Step 1: Lint**

```bash
mise exec -- just lint; echo "exit: $?"
```
Expected: `exit: 0`, 0 issues.

- [x] **Step 2: Full uncached suite with Docker running**

```bash
mise exec -- go test -count=1 ./... 2>&1 | tail -15; echo "exit: $?"
```
Expected: `exit: 0`, all packages ok, **0 skips**. Check `jj st` for stray `testdata/rapid` failfiles; investigate any (replay the shrunk input — distinguish a Kurrent connection flake from a real grammar finding), then delete.

- [x] **Step 3: CodeRabbit pass over the phase**

```bash
cr review --plain 2>&1 | tail -30
```
Address Important findings; record dismissed nits with reasons.

- [x] **Step 4: Coordinator commits any review fixes** (exact fileset per fix, past tense), or nothing if clean.

---

## Phase 2 — Item D: align wee-events.rs (requires Phase 1)

Repo: `~/Projects/weegigs/wee-events.rs`. Coordinator checks VC flavour
first (`ls ~/Projects/weegigs/wee-events.rs/.jj 2>/dev/null || echo git`) and
uses jj split there if colocated, plain `git add`/`git commit` otherwise —
same message rules.

### Task 9: Vendor vectors + failing conformance test

**Files (in wee-events.rs):**
- Create: `crates/wee-events/tests/vectors/aggregate-identity.vectors.json` (verbatim copy)
- Create: `crates/wee-events/tests/identity_vectors.rs`
- Modify: `crates/wee-events/Cargo.toml` (dev-dependencies, only if `serde_json` absent)

- [x] **Step 1: Copy the vector file verbatim**

```bash
mkdir -p ~/Projects/weegigs/wee-events.rs/crates/wee-events/tests/vectors
cp /Users/kevin/Projects/weegigs/wee-events-go/documents/spec/aggregate-identity.vectors.json \
   ~/Projects/weegigs/wee-events.rs/crates/wee-events/tests/vectors/aggregate-identity.vectors.json
```

- [x] **Step 2: Ensure `serde_json` is a dev-dependency**

```bash
grep -q 'serde_json' ~/Projects/weegigs/wee-events.rs/crates/wee-events/Cargo.toml || (cd ~/Projects/weegigs/wee-events.rs && cargo add --dev serde_json -p wee-events)
```

- [x] **Step 3: Write `crates/wee-events/tests/identity_vectors.rs`**:

```rust
//! Conformance against the shared aggregate-identity grammar
//! (wee-events-go: documents/spec/aggregate-identity.md, spec v1).
//! The vendored vector file is a verbatim copy; the pinned version below
//! fails this suite visibly when the master file moves.

use serde::Deserialize;
use wee_events::{AggregateId, AggregateIdParseError};

const VECTORS: &str = include_str!("vectors/aggregate-identity.vectors.json");
const EXPECTED_VERSION: u32 = 1;

#[derive(Deserialize)]
struct VectorFile {
    spec: String,
    version: u32,
    construct: Vec<ConstructVector>,
    parse: Vec<ParseVector>,
}

#[derive(Deserialize)]
struct ConstructVector {
    #[serde(rename = "type")]
    aggregate_type: String,
    key: String,
    valid: bool,
    #[serde(default)]
    reason: String,
}

#[derive(Deserialize)]
struct ParseVector {
    input: String,
    valid: bool,
    #[serde(default, rename = "type")]
    aggregate_type: String,
    #[serde(default)]
    key: String,
    #[serde(default)]
    reason: String,
}

fn load() -> VectorFile {
    let file: VectorFile = serde_json::from_str(VECTORS).expect("vector file parses");
    assert_eq!(file.spec, "aggregate-identity");
    assert_eq!(
        file.version, EXPECTED_VERSION,
        "vendored vector file version moved — re-vendor and re-align"
    );
    assert!(!file.construct.is_empty() && !file.parse.is_empty());
    file
}

fn assert_reason(reason: &str, err: &AggregateIdParseError, context: &str) {
    use AggregateIdParseError::*;
    let matches = matches!(
        (reason, err),
        ("missing-separator", MissingColon)
            | ("empty-type", EmptyType)
            | ("empty-key", EmptyKey)
            | ("invalid-type", InvalidType)
            | ("invalid-key", InvalidKey)
    );
    assert!(matches, "{context}: expected reason {reason:?}, got {err:?}");
}

#[test]
fn construct_vectors() {
    for v in load().construct {
        let context = format!("construct {}:{}", v.aggregate_type, v.key);
        match AggregateId::try_new(&v.aggregate_type, &v.key) {
            Ok(id) => {
                assert!(v.valid, "{context}: accepted but vector says invalid");
                assert_eq!(id.aggregate_type().as_str(), v.aggregate_type, "{context}");
                assert_eq!(id.aggregate_key(), v.key, "{context}");
            }
            Err(err) => {
                assert!(!v.valid, "{context}: rejected ({err:?}) but vector says valid");
                assert_reason(&v.reason, &err, &context);
            }
        }
    }
}

#[test]
fn parse_vectors() {
    for v in load().parse {
        let context = format!("parse {:?}", v.input);
        match v.input.parse::<AggregateId>() {
            Ok(id) => {
                assert!(v.valid, "{context}: accepted but vector says invalid");
                assert_eq!(id.aggregate_type().as_str(), v.aggregate_type, "{context}");
                assert_eq!(id.aggregate_key(), v.key, "{context}");
                assert_eq!(id.to_string(), v.input, "{context}: round-trip is byte-for-byte");
            }
            Err(err) => {
                assert!(!v.valid, "{context}: rejected ({err:?}) but vector says valid");
                assert_reason(&v.reason, &err, &context);
            }
        }
    }
}
```

- [x] **Step 4: Run to verify it fails** (red — `try_new`, `InvalidType`, `InvalidKey` don't exist yet)

```bash
cd ~/Projects/weegigs/wee-events.rs && cargo test -p wee-events --test identity_vectors 2>&1 | tail -10
```
Expected: COMPILE ERROR naming `try_new` / `InvalidType` / `InvalidKey`.

(No commit yet — red state; Task 10 completes it.)

### Task 10: Implement grammar v2 in `crates/wee-events/src/id.rs`

**Files (in wee-events.rs):**
- Modify: `crates/wee-events/src/id.rs`

- [x] **Step 1: Add the validators and constants** (module level, near `AggregateId`):

```rust
/// Length caps in octets (wee-events-go: documents/spec/aggregate-identity.md).
const MAX_TYPE_OCTETS: usize = 64;
const MAX_KEY_OCTETS: usize = 512;

/// Type grammar: kebab-case tokens of [a-z0-9] joined by single hyphens,
/// first token starting with a letter, at most 64 octets.
fn valid_identity_type(s: &str) -> bool {
    let bytes = s.as_bytes();
    if bytes.is_empty() || bytes.len() > MAX_TYPE_OCTETS || !bytes[0].is_ascii_lowercase() {
        return false;
    }
    let mut previous_hyphen = false;
    for &b in &bytes[1..] {
        match b {
            b'a'..=b'z' | b'0'..=b'9' => previous_hyphen = false,
            b'-' => {
                if previous_hyphen {
                    return false;
                }
                previous_hyphen = true;
            }
            _ => return false,
        }
    }
    !previous_hyphen
}

/// Key grammar: segments of [A-Za-z0-9._@-] joined by single pipes, at most
/// 512 octets, and the whole key is never "." or ".." (URL dot-segment rule
/// — whole-key only; interior dots are opaque data).
fn valid_identity_key(s: &str) -> bool {
    if s.is_empty() || s.len() > MAX_KEY_OCTETS || s == "." || s == ".." {
        return false;
    }
    let mut previous_boundary = true;
    for &b in s.as_bytes() {
        match b {
            b'a'..=b'z' | b'A'..=b'Z' | b'0'..=b'9' | b'-' | b'.' | b'_' | b'@' => {
                previous_boundary = false
            }
            b'|' => {
                if previous_boundary {
                    return false;
                }
                previous_boundary = true;
            }
            _ => return false,
        }
    }
    !previous_boundary
}
```

- [x] **Step 2: Extend `AggregateIdParseError`** with the two new variants (after `EmptyKey`):

```rust
    #[error("aggregate type violates the identity grammar (lowercase kebab, ≤64 octets)")]
    InvalidType,
    #[error("aggregate key violates the identity grammar (pipe-joined segments of [A-Za-z0-9._@-], ≤512 octets, never '.' or '..')")]
    InvalidKey,
```

- [x] **Step 3: Add `try_new` to `impl AggregateId`** (after `new`):

```rust
    /// Validating constructor for untrusted parts, enforcing the shared
    /// identity grammar (wee-events-go:
    /// documents/spec/aggregate-identity.md). Emptiness is reported with its
    /// own variant; every other violation is `InvalidType` / `InvalidKey`.
    pub fn try_new(
        aggregate_type: impl AsRef<str>,
        aggregate_key: impl AsRef<str>,
    ) -> Result<Self, AggregateIdParseError> {
        let (aggregate_type, aggregate_key) = (aggregate_type.as_ref(), aggregate_key.as_ref());
        if aggregate_type.is_empty() {
            return Err(AggregateIdParseError::EmptyType);
        }
        if aggregate_key.is_empty() {
            return Err(AggregateIdParseError::EmptyKey);
        }
        if !valid_identity_type(aggregate_type) {
            return Err(AggregateIdParseError::InvalidType);
        }
        if !valid_identity_key(aggregate_key) {
            return Err(AggregateIdParseError::InvalidKey);
        }
        Ok(Self {
            aggregate_type: AggregateType::new(aggregate_type),
            aggregate_key: aggregate_key.to_string(),
        })
    }
```

- [x] **Step 4: Route `FromStr` through the same validation** — replace the body's final `Ok(...)` construction:

```rust
    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let (agg_type, agg_key) = s
            .split_once(':')
            .ok_or(AggregateIdParseError::MissingColon)?;
        Self::try_new(agg_type, agg_key)
    }
```

- [x] **Step 5: Fix the doc comments that bless the old behaviour.** In the `AggregateId` "Wire format" doc comment, replace the sentence claiming the key "may contain additional colons for compound identifiers like `"run:01ABC"` or URN-style values" with:

```rust
/// it is the key. Under the shared identity grammar the canonical form
/// contains exactly one colon; composite keys use `|`-joined segments
/// (`"run|01ABC"`). The grammar is normative in the wee-events-go
/// repository: `documents/spec/aggregate-identity.md`.
```

Update the construction table row for `FromStr`/`TryFrom` if it names only the old errors, and extend the `new()` trust note: `new()` still trusts typed parts (the `SocketAddrV4` pattern); `try_new` is the validating counterpart for untrusted parts.

- [x] **Step 6: Run the conformance and full crate tests**

```bash
cd ~/Projects/weegigs/wee-events.rs && cargo test -p wee-events 2>&1 | tail -10
```
Expected: PASS including `identity_vectors`. Pre-existing unit tests constructing identities outside grammar v2 (uppercase types, colon keys) fail here — update each to a v2-valid spelling, or where the test pins the *old* permissive behaviour, replace it with the new rejection assertion.

- [x] **Step 7: Lint/format gates**

```bash
cd ~/Projects/weegigs/wee-events.rs && cargo clippy -p wee-events --all-targets 2>&1 | tail -5 && cargo fmt --check
```
Expected: no warnings (no `#[allow]` — house rule), clean format.

- [x] **Step 8: Coordinator commits in wee-events.rs** (one commit, full fileset of Tasks 9+10):

Message: `Aligned AggregateId validation to the shared identity grammar v2 and bound it to the vendored conformance vectors`

### Task 11: Record Rust conformance in the Go repo

**Files:**
- Modify: `documents/spec/aggregate-identity.md` (status table)
- Modify: `documents/roadmap.md` (remove the item-D follow-up)

- [x] **Step 1:** In the spec's conformance table, change the Rust row to `| Rust (\`wee-events.rs\`) | Conformant (vendored vectors, spec v1) |`.
- [x] **Step 2:** In `documents/roadmap.md` "Follow-ups discovered during implementation", delete the "Align `wee-events.rs` to the tightened identity charset" bullet entirely (it is done, not amended).
- [x] **Step 3: Coordinator commits**

```bash
jj split documents/spec/aggregate-identity.md documents/roadmap.md -m "Recorded wee-events.rs conformance to the shared identity grammar and closed the alignment follow-up"
```

---

## Phase 3 — Item E: Restate harness check (evidence already gathered)

### Task 12: Record still-blocked with evidence

`go list -m -versions github.com/restatedev/sdk-go` (run 2026-06-10) shows
v0.24.0 is still the latest — no release adopting the testcontainers-go
v0.42+ `network.Port` API exists. No code change is possible.

**Files:**
- Modify: `documents/roadmap.md` (the sdk-go follow-up bullet)

- [x] **Step 1:** Append to the existing "`restatedev/sdk-go` test harness vs `testcontainers-go` v0.42" bullet:

```markdown
  Checked 2026-06-10: v0.24.0 remains the latest release; still blocked.
```

- [x] **Step 2: Coordinator commits**

```bash
jj split documents/roadmap.md -m "Recorded the sdk-go harness follow-up as still blocked at v0.24.0 (checked 2026-06-10)"
```

---

## Phase 4 — Item A: envelope opacity (`we.Data.Data` → `[]byte`)

Owner decision recorded: previously stored ds/jetstream development events
become unreadable (envelope shape changes) and are **orphaned, not
migrated** — confirmed precedent (ADR-0010 restates it). Docker required.

### Task 13: Retype the field

**Files:**
- Modify: `we/event.go:27–30`

- [x] **Step 1: Retype** — replace the `Data` struct with:

```go
// Data is the encoding-tagged payload envelope. Data is opaque bytes in the
// payload's own encoding; in JSON envelope serialisations it appears as
// base64 (encoding/json's []byte form), which is what lets JSON-envelope
// stores (ds, jetstream) carry any payload encoding losslessly.
type Data struct {
	Encoding string `json:"encoding"`
	Data     []byte `json:"data"`
}
```

Remove the now-unused `"encoding/json"` import from `we/event.go` if nothing else in the file uses it.

- [x] **Step 2: Build and run the core suite**

```bash
mise exec -- go build ./... && mise exec -- go test -count=1 ./we/ ./samples/... ./connectors/wehttp/ 2>&1 | tail -10; echo "exit: $?"
```
Expected: compiles everywhere (`json.RawMessage` is `[]byte` underneath, so codec assignments are unchanged). Test failures, if any, are envelope-shape assertions — update each to expect base64 (`"data":"eyJ..."` instead of an embedded object), asserting the *decoded* payload value rather than raw envelope text wherever possible.

- [x] **Step 3: Coordinator commits** (include any assertion updates in the fileset)

```bash
jj split we/event.go -m "Retyped we.Data.Data to []byte so envelope serialisation treats payload bytes as opaque"
```

### Task 14: Flip the ds/jetstream loud-failure tests to round-trips

The loud-failure contract those tests pin (CBOR override fails on
JSON-envelope stores) disappears with the retype — the same scenarios now
succeed and must prove the round-trip instead.

**Files:**
- Modify: `stores/jetstream/encoding_test.go` (the "cbor override … fails loudly" subtest, comment block lines ~93–109)
- Modify: `stores/ds/event-store_test.go` (the "cbor override takes precedence over the json constructor encoder" subtest, comment block lines ~75–94)

- [x] **Step 1: jetstream** — replace the loud-failure subtest and its comment with:

```go
	// ENCODING-S2.R3 — the per-publish override takes precedence over the
	// constructor encoder; with the envelope treating payload bytes as
	// opaque, a CBOR override on a JSON-constructed store round-trips
	// end-to-end.
	t.Run("cbor override round-trips on the json constructor store", func(t *testing.T) {
		id := testIdentity(t)
		require.NoError(t, jsonStore.Publish(ctx, id, we.Options(we.WithEncoder(we.MakeCBOREncoder())), encodingTestEvent{Value: "cbor"}))

		loaded, err := jsonStore.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, loaded.Events, 1)
		require.Equal(t, we.CBOREncoding, loaded.Events[0].Data.Encoding)

		var decoded encodingTestEvent
		require.NoError(t, we.MakeCBORDecoder().Decode(loaded.Events[0].Data, &decoded))
		require.Equal(t, "cbor", decoded.Value)
	})
```

(Mirror the identity helper and load/assert pattern of the file's existing positive-path subtest exactly — the JSON-override subtest a few lines below already loads and decodes recorded events; use its exact accessor shapes.)

- [x] **Step 2: ds** — same transformation for the ds subtest, mirroring the file's existing positive-path load/assert pattern with `we.CBOREncoding` and a `CBORDecoder` decode.

- [x] **Step 3: Run both packages (Docker running)**

```bash
mise exec -- go test -count=1 ./stores/ds/ ./stores/jetstream/ 2>&1 | tail -8; echo "exit: $?"
```
Expected: `exit: 0`.

- [x] **Step 4: Coordinator commits**

```bash
jj split stores/ds/event-store_test.go stores/jetstream/encoding_test.go -m "Flipped the ds/jetstream CBOR-override tests from loud failure to end-to-end round-trip after the envelope retype"
```

### Task 15: Conformance scenario — CBOR payloads round-trip on every backend

**Files:**
- Modify: `we/event-store-validation-suite.go` (scenario list + new method)

- [x] **Step 1: Register the scenario** in the suite's scenario table (alongside `{"round-trips full-charset identities through storage", s.IdentityRoundTripsThroughStorage}`):

```go
		{"round-trips cbor payloads through storage", s.CBORPayloadRoundTripsThroughStorage},
```

- [x] **Step 2: Implement the method** (the suite's API, per `IdentityRoundTripsThroughStorage` in the same file: `s.store`, `s.ctx`, `s.MakeTestAggregateId()`, `StoreValidationEvent`, `Load` returning a value with `.Events`):

```go
// CBORPayloadRoundTripsThroughStorage proves a backend stores and returns
// CBOR payload bytes verbatim regardless of its envelope serialisation
// (ENCODING-S3.R2 is unscoped: every backend carries every encoding). The
// per-publish override exercises the path without constructor changes.
func (s *EventStoreValidationSuite) CBORPayloadRoundTripsThroughStorage(t *testing.T) {
	id := s.MakeTestAggregateId()
	event := StoreValidationEvent{TestStringValue: "opaque", TestIntValue: 42}
	require.NoError(t, s.store.Publish(s.ctx, id, Options(WithEncoder(MakeCBOREncoder())), event))

	loaded, err := s.store.Load(s.ctx, id)
	require.NoError(t, err)
	require.Len(t, loaded.Events, 1)
	require.Equal(t, CBOREncoding, loaded.Events[0].Data.Encoding)

	var decoded StoreValidationEvent
	require.NoError(t, MakeCBORDecoder().Decode(loaded.Events[0].Data, &decoded))
	require.Equal(t, event, decoded, "cbor payload must round-trip verbatim")
}
```

- [x] **Step 3: Run the suite across all backends (Docker running)**

```bash
mise exec -- go test -count=1 ./we/ ./stores/... 2>&1 | tail -10; echo "exit: $?"
```
Expected: `exit: 0` — memory, ds, jetstream, kurrent, sqlite all pass the new scenario, 0 skips.

- [x] **Step 4: Coordinator commits**

```bash
jj split we/event-store-validation-suite.go -m "Extended the conformance suite with a CBOR payload round-trip scenario binding every backend (ENCODING-S3.R2 unscoped)"
```

### Task 16: Sweep the now-stale contracts

**Files:**
- Modify: `stores/ds/event-store.go` (godoc caveat, lines ~36–39)
- Modify: `stores/jetstream/jetstream.go` (godoc caveat, lines ~23–26)
- Modify: `documents/features/08-explicit-event-encoding.md` (ENCODING-S3.R2 scoping note + verification row)
- Modify: `documents/roadmap.md` (remove the item-A follow-up bullet)

- [x] **Step 1:** In both store godocs, delete the caveat sentences stating a CBOR-constructed/overridden store "fails every non-empty publish loudly at serialization — end-to-end CBOR is scoped to BLOB-backed stores (ENCODING-S3.R2)". Replace with one sentence:

```go
// Payload bytes are opaque in the persisted envelope (base64 in JSON), so
// every payload encoding round-trips end-to-end (ENCODING-S3.R2).
```

- [x] **Step 2:** In feature 08, ENCODING-S3.R2: delete the italic scoping note "*(End-to-end CBOR remains scoped to BLOB-backed stores … verified behaviour.)*" and update the verification-table row for ENCODING-S3.R2 to: `conformance suite "round-trips cbor payloads through storage" across memory/ds/jetstream/kurrent/sqlite`.
- [x] **Step 3:** In `documents/roadmap.md`, delete the "`we.Data.Data` is typed `json.RawMessage` but now carries CBOR bytes" follow-up bullet entirely.
- [x] **Step 4:** Verify no stale claims survive:

```bash
grep -rn "scoped to BLOB\|BLOB-backed" --include="*.go" --include="*.md" . | grep -v docs/superpowers
```
Expected: no output (or only historical docs under `docs/superpowers/`).

- [x] **Step 5: Phase gate** — lint + full suite + CodeRabbit, exactly as Task 8.
- [x] **Step 6: Coordinator commits**

```bash
jj split stores/ds/event-store.go stores/jetstream/jetstream.go documents/features/08-explicit-event-encoding.md documents/roadmap.md -m "Removed the BLOB-only CBOR scoping caveats and closed the envelope-opacity follow-up"
```

---

## Phase 5 — Item B: Kurrent reconnect investigation (checkpoint, no implementation)

### Task 17: Investigate and report — STOP at owner decision

The pinned client (`github.com/kurrent-io/KurrentDB-Client-Go v1.2.0`) is
the latest release; there is no upgrade path. This task produces findings,
not code. **Do not design or implement a fix in this plan.**

- [x] **Step 1: Locate the client source**

```bash
ls $(mise exec -- go env GOMODCACHE)/github.com/kurrent-io/
```

- [x] **Step 2: Investigate, answering exactly these questions** (read `connection`/`client`/`grpc` files in the module):
  1. Where is `ErrorCodeConnectionClosed` produced, and what does the client do with the underlying `grpc.ClientConn` afterwards?
  2. Does any code path re-dial or rebuild the channel after a terminal connection error (search for `Dial`, `reconnect`, `discover`)?
  3. Do `kurrentdb.Configuration`/client options expose keepalive, retry, or reconnection knobs the store could enable?
  4. Is the poisoning a client bug (dead conn cached) or documented behaviour (caller must rebuild the client)?
- [x] **Step 3: Check upstream issues** for known reports (`gh search issues --repo kurrent-io/KurrentDB-Client-Go "connection closed"` — best effort, skip if offline).
- [x] **Step 4: Update the roadmap follow-up bullet** with the findings (facts only — what the client does, what knobs exist, what upstream says), and present them to the owner with a recommendation between the two roadmap options (re-dial on detection in `stores/kurrent` vs typed connection-state error for caller rebuild). **STOP — the fix is a follow-on plan after the owner decides.**
- [x] **Step 5: Coordinator commits** the roadmap update:

```bash
jj split documents/roadmap.md -m "Recorded the KurrentDB client reconnect investigation findings"
```

---

## Definition of done (from the handoff)

- Items C and D implemented and gated; E recorded as still-blocked with dated evidence; A implemented and gated; B investigated with findings recorded and an owner checkpoint reached.
- Roadmap follow-ups section reflects reality: A and D bullets removed, E dated, B carries findings.
- All gates green **run in-session**: `mise exec -- just lint` exit 0; `mise exec -- go test -count=1 ./...` exit 0 with 0 skips (Docker running); `cargo test -p wee-events` green in wee-events.rs; restate integration green (`go test -tags integration ./connectors/werestate/`).
- No stray `testdata/rapid` failfiles in `jj st`.
- Report with evidence: gate transcripts and the commit list. Never claim green without running the command in the same session.
