# Feature Backlog

This directory holds the implementation backlog for `wee-events-go`. Each feature is
specified in its own document so the work can proceed in parallel with minimal
file-level contention. Read this overview first, then pick a feature document.

Features fall into two kinds:

- **Ports** — capabilities that already exist in a sibling implementation (currently
  `wee-events.rs`, the most recent member of the `wee-events` family) and are being
  brought across to Go. The first batch (Docs 01–05) are all ports.
- **Net-new** — features with no sibling precedent, designed here first. None yet;
  they follow the same document template, minus the "Rust reference" section.

The remainder of this document covers the current porting batch.

## Source comparison basis

The current batch's gap was established by reading both trees:

- Go core: `we/` — notably `event-store.go`, `data-marshaller.go`,
  `command.go`, `service.go`, `dispatcher.go`, `event-store-validation-suite.go`
- Go stores: `stores/{ds,jetstream,kurrent}`
- Rust core: `wee-events.rs/crates/wee-events/src/{codec,store,test_suite,service,event}.rs`
- Rust extensions: `wee-events.rs/crates/wee-events-{sqlite,restate,macros}`

## Capability gap

| Capability | Rust (have) | Go (current) | Document |
|---|---|---|---|
| Event serialization | JSON + CBOR; codec-agnostic store (`encoding` + `data BLOB`); polymorphic decoder chain | JSON only, hardcoded in `MarshalToData` | [01](01-cbor-codec.md) |
| SQLite / libSQL / Turso store | `wee-events-sqlite` | none | [02](02-sqlite-turso-store.md) |
| Restate durable execution | `wee-events-restate` | none | [03](03-restate-integration.md) |
| Storage conformance tests | 14 single-store + 2 shared-backing scenarios | 11 single-store scenarios | [04](04-storage-verification-tests.md) |
| Structured rejections | `ServiceError` = `Rejection \| Store \| Codec` | bare `error` from handlers | [05](05-rejection-error-taxonomy.md) |

### Explicitly out of scope

- **Projections / document store.** Rust's `wee-events-sqlite` carries a `documents`
  table plus `apply_projection` / `rebuild_projection`. This is excluded by request.
  The Go SQLite/Turso store (Doc 02) is **events-only** — a single `events` table,
  no projection sink.
- **Compile-time command dispatch (`Handles<C>`).** Rust enforces "service handles
  command C" at compile time through trait bounds. Go has no equivalent affordance;
  its dispatcher is a runtime `map[CommandName]CommandHandler`. Reproducing this in
  Go would mean faking a guarantee the language cannot give, so it is left as-is.

## Dependency graph and sequencing

```
        ┌────────────────────────────────────────────┐
        │  Doc 01  pluggable codec + CBOR (we/)        │ foundational
        └───────────────┬────────────────────────────┘
                        │ shares we/command.go
        ┌───────────────┴────────────────────────────┐
        │  Doc 05  rejection / error taxonomy (we/)    │
        └─────────────────────────────────────────────┘

  Doc 02  SQLite/Turso store (stores/sqlite/)   ─┐
  Doc 03  Restate connector (connectors/werestate/) ─┤ independent, parallel-safe
  Doc 04  conformance suite (we/ + */**_test.go) ─┘
```

- **Docs 02, 03, 04 are independent.** Their primary files are disjoint and may be
  worked simultaneously by separate owners.
- **Docs 01 and 05 both edit `we/command.go`.** This is the only file-level overlap.
  Land Doc 01 first, then Doc 05 — or assign both to a single owner.
- **Docs 01 and 04 are foundational but not blocking.** Every new store benefits from
  the codec seam (01) and the broader conformance suite (04). New stores can target
  the existing `application/json` path until Doc 01 lands; the SQLite store (02)
  should validate against Doc 04's enhanced suite once available, but can start
  against the current suite.

## File-ownership matrix

| Doc | Owns (writes) | Notes |
|---|---|---|
| 01 | `we/codec.go` (new), `we/data-marshaller.go`, `we/command.go` | core; coordinates with 05 on `command.go` |
| 02 | `stores/sqlite/**` (new) | new package, isolated |
| 03 | `connectors/werestate/**` (new) | new package, isolated |
| 04 | `we/event-store-validation-suite.go`; each store's own `*_test.go` | store-test edits are owned by each store's worker |
| 05 | `we/rejection.go` (new), `we/command.go`, `we/service.go`, `we/dispatcher.go`, `connectors/wehttp/http.go` | core; coordinates with 01 on `command.go` |

## Feature document model

A feature document is an **epic**: an epic-level requirement decomposed into a series of
**end-to-end user stories**, each carrying its acceptance criteria as **EARS
requirements** and cross-referencing the **ADRs** that constrain it.

A worked exemplar is [01-cbor-codec.md](01-cbor-codec.md). Each epic document has:

1. **Header** — status, size (T-shirt), area, coordination notes.
2. **Summary** — the epic-level requirement and the user-facing outcome.
3. **Decisions** — links to the [ADRs](../adr/README.md) this epic depends on. The epic
   references decisions; it does not restate them.
4. **User stories** — a numbered series. Each story is end-to-end (delivers observable
   value on its own) and contains:
   - a narrative — *"As a ⟨role⟩, I want ⟨capability⟩, so that ⟨benefit⟩."*
   - acceptance criteria as EARS requirements (below), each with an ID.
5. **Implementation notes** — supporting detail: source references (for ports, the Rust
   origin), target Go files, libraries.
6. **Verification** — how the requirements are proven, mapped back to requirement IDs.

### EARS requirement syntax

Every requirement uses one [EARS](https://alistairmavin.com/ears/) template so it stays
atomic and testable:

| Pattern | Template |
|---|---|
| Ubiquitous | The framework shall ⟨response⟩. |
| Event-driven | When ⟨trigger⟩, the framework shall ⟨response⟩. |
| State-driven | While ⟨state⟩, the framework shall ⟨response⟩. |
| Unwanted behaviour | If ⟨condition⟩, then the framework shall ⟨response⟩. |
| Optional feature | Where ⟨feature is included⟩, the framework shall ⟨response⟩. |

"Unwanted behaviour" requirements are the negative tests — each should map to a failing
case in the test plan.

### Requirement IDs

IDs encode the containment hierarchy so a cross-reference or a test failure points at one
line: `⟨FEATURE⟩-S⟨story⟩.R⟨req⟩`. Example: `CBOR-S2.R1` is feature CBOR, story 2,
requirement 1. Feature prefixes: `CBOR` (01), `SQLITE` (02), `RESTATE` (03), `CONFORMANCE`
(04), `REJECT` (05).

## Conventions and principles

Feature work is held to the repo's [design principles](../principles.md) — single
responsibility, explicit resource lifecycle (the Go form of RAII), and illegal-states-
unrepresentable within Go's limits — and to the mechanical [conventions](../conventions.md).
A user story should note which principle its acceptance criteria uphold; an
unwanted-behaviour EARS requirement that rejects an invalid construction is principle 3 in
action.

Also per `documents/conventions.md` and the repo `CLAUDE.md`:

- `New*` constructors return a pointer; `Make*` return a value.
- Logging is `info` (user-facing) and `debug` (developer) only.
- Complexity is expressed with T-shirt sizing (XS/S/M/L/XL), never time estimates.
- Verification is by running tests, not by assertion; each requirement is falsifiable.
