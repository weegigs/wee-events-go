# Architecture Decision Records

An ADR captures a single architectural decision: the context that forced a choice, the
decision taken, and its consequences. ADRs are **append-only and numbered** — once
accepted, an ADR is not edited to reflect a new decision; instead a new ADR supersedes
it.

Feature documents under [`../features/`](../features/README.md) *reference* ADRs rather
than restating decisions, so a decision lives in exactly one place. When a decision
changes, supersede its ADR and the references still resolve.

## Index

| # | Title | Status |
|---|---|---|
| [0001](0001-default-event-encoding-json.md) | JSON is the default event encoding | Accepted |
| [0002](0002-cbor-library-fxamacker.md) | Use `fxamacker/cbor/v2` for CBOR | Accepted |
| [0003](0003-sqlite-driver-libsql.md) | Use `go-libsql` for the SQLite/libSQL store | Accepted |
| [0004](0004-restate-go-sdk.md) | Use the Restate Go SDK and wire dispatch manually | Accepted |
| [0005](0005-rejection-error-modeling.md) | Model domain rejections via `error` + `errors.As` | Accepted |
| [0006](0006-lint-enforcement.md) | Enforce resource-lifecycle principles via golangci-lint | Accepted |

## Conventions

- **Filename:** `NNNN-kebab-title.md`, zero-padded to four digits, allocated
  sequentially.
- **Status:** `Proposed` → `Accepted` → (`Superseded by NNNN` | `Deprecated`). A
  decision still under discussion in a feature document is `Proposed` until its work
  begins.
- **One decision per ADR.** If a feature surfaces several decisions, write several ADRs.
- **Cross-referencing:** features link to ADRs by number/path; ADRs may list the
  features and other ADRs they affect.
- Start from [`template.md`](template.md).
