# Architecture Decision Records

An ADR captures a single architectural decision: the context that forced a choice, the
decision taken, and its consequences. ADRs are **numbered and never edited into a new
decision** — when a decision changes, a new ADR supersedes the old one. The superseding
ADR restates whatever survives (context, rationale, constraints), and the superseded
file is then **removed from the repository**: a dead decision left lying around is a
distractor — for people and for agents — and version-control history retains its full
text. Numbers are never reused; the index below keeps a one-line tombstone.

Feature documents under [`../features/`](../features/README.md) *reference* ADRs rather
than restating decisions, so a decision lives in exactly one place. When a decision
changes, supersede its ADR and the references still resolve.

## Index

| # | Title | Status |
|---|---|---|
| 0001 | JSON is the default event encoding | Superseded by 0007 — removed |
| [0002](0002-cbor-library-fxamacker.md) | Use `fxamacker/cbor/v2` for CBOR | Accepted |
| [0003](0003-sqlite-driver-libsql.md) | Use `go-libsql` for the SQLite/libSQL store | Accepted |
| [0004](0004-restate-go-sdk.md) | Use the Restate Go SDK and wire dispatch manually | Accepted |
| [0005](0005-rejection-error-modeling.md) | Model domain rejections via `error` + `errors.As` | Accepted |
| [0006](0006-lint-enforcement.md) | Enforce resource-lifecycle principles via golangci-lint | Accepted |
| [0007](0007-explicit-event-encoding.md) | Event encoding is an explicit constructor argument | Accepted |
| [0008](0008-aggregate-identity.md) | Aggregate identity: canonical `type:key` form and validated construction | Accepted |
| [0009](0009-property-based-testing-rapid.md) | Use `pgregory.net/rapid` for property-based conformance testing | Accepted |

## Conventions

- **Filename:** `NNNN-kebab-title.md`, zero-padded to four digits, allocated
  sequentially.
- **Status:** `Proposed` → `Accepted` → (`Superseded by NNNN` | `Deprecated`). A
  decision still under discussion in a feature document is `Proposed` until its work
  begins. On supersession, the new ADR restates what survives, every reference is
  repointed, and the superseded file is deleted (tombstone row stays in the index;
  history holds the text).
- **One decision per ADR.** If a feature surfaces several decisions, write several ADRs.
- **Cross-referencing:** features link to ADRs by number/path; ADRs may list the
  features and other ADRs they affect.
- Start from [`template.md`](template.md).
