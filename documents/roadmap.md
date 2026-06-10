# Roadmap

This roadmap tracks planned work on `wee-events-go`. Each feature is specified in its
own document under [`features/`](features/README.md), scoped so the work can proceed in
parallel. This is the at-a-glance index and sequencing view; the feature documents hold
the detail (target files, libraries, success criteria, test plans).

Features are either **ports** (capabilities brought across from a sibling
implementation — currently `wee-events.rs`) or **net-new** (designed here first). The
current batch (01–05) are all ports closing the gap to the Rust implementation; future
net-new work lands in the same backlog.

All feature work is held to the repo [design principles](principles.md) — single
responsibility, explicit resource lifecycle (the Go form of RAII), and illegal-states-
unrepresentable within Go's limits.

## Status

| # | Feature | Area | Size | Status |
|---|---|---|---|---|
| [01](features/01-cbor-codec.md) | Pluggable codec + CBOR support | core `we/` | M | Done |
| [02](features/02-sqlite-turso-store.md) | SQLite / libSQL / Turso event store | new `stores/sqlite/` | L | Done |
| [03](features/03-restate-integration.md) | Restate durable-execution connector | new `connectors/werestate/` | L | Done |
| [04](features/04-storage-verification-tests.md) | Storage conformance test parity | `we/` + per-store tests | M | Done |
| [05](features/05-rejection-error-taxonomy.md) | Structured rejection / error taxonomy | core `we/` + `wehttp` | M | Done |
| [07](features/07-aggregate-identity.md) | Aggregate identity: canonical form + validated construction | core `we/` + stores + connectors | M | Ready |
| [08](features/08-explicit-event-encoding.md) | Explicit event encoding (no implicit default) | core `we/` + stores + samples | M | Ready |
| [09](features/09-error-surfacing.md) | Error surfacing: no fabricated values | core + stores + connectors + samples | M | Ready |

Size is T-shirt complexity (XS/S/M/L/XL), not a time estimate.

To execute this backlog, [`implementation-kickoff.md`](implementation-kickoff.md) briefs a
coordinating agent that delegates each feature to a worker and integrates them in the order
below.

## Future (unscheduled)

Captured but **not scheduled** — not part of the 01–05 batch, not assigned to a release, and
not in the sequencing graph below. Recorded so the intent and design constraints are not lost.

| # | Feature | Area | Size | Status |
|---|---|---|---|---|
| [06](features/06-payload-encryption.md) | Application-level payload encryption | core `we/` (codec) | L? | Future (unscheduled) |

Feature 06 records the replacement for the removed `PublishOptions.Encrypt` flag (an
unimplemented field deleted on the candor / no-meaningless-states rule). It is per-payload
encryption at the codec layer — distinct from the database-at-rest encryption noted in
[ADR-0003](adr/0003-sqlite-driver-libsql.md). It depends on Feature 01's codec seam and needs
an ADR for its key model before it could be scheduled.

## Sequencing

```
  01 codec ──▶ 05 rejections      (both edit we/command.go — sequence or co-own)

  02 sqlite/turso store  ─┐
  03 restate connector   ─┤  independent — run in parallel
  04 conformance suite   ─┘

  07 identity ──▶ 09 error surfacing   (both edit restate.go + http.go — sequence or co-own)
  08 explicit encoding                 (independent of 07/09 at function level; shares
                                        store files — coordinate merges)
```

- **Docs 02, 03, 04 are independent** and parallel-safe; their primary files are
  disjoint.
- **Docs 01 and 05 share `we/command.go`** — the only file-level overlap in the
  backlog. Land 01 first, then 05, or assign both to one owner.
- **01 and 04 are foundational but non-blocking.** New stores benefit from the codec
  seam (01) and the broader conformance suite (04), but can start against the existing
  JSON path and current suite until those land. The SQLite store (02) should validate
  against the enhanced suite (04) once available.

## Out of scope

- **Projections / document store.** Rust's SQLite crate carries a `documents` table and
  `apply`/`rebuild` projection helpers; these are excluded. The Go SQLite/Turso store
  (02) is events-only.
- **Compile-time command dispatch (`Handles<C>`).** A Rust trait-bound affordance with
  no honest Go equivalent; the Go dispatcher stays a runtime route map.

## Follow-ups discovered during implementation

Surfaced by the 01–05 work and its reviews; recorded for later, not yet scheduled.

- **`we.Data.Data` is typed `json.RawMessage` but now carries CBOR bytes.** Feature 01 made
  payloads codec-agnostic, but the `Data` envelope field is still `json.RawMessage`. Stores
  that persist the *whole envelope as JSON* (`stores/jetstream`, `stores/ds`) would emit
  invalid JSON for a CBOR payload, because `json.RawMessage` is copied verbatim into the JSON
  output. `stores/kurrent` and `stores/sqlite` store the payload as an opaque BLOB and are
  unaffected, so end-to-end CBOR is currently safe only on those two. Fix: retype `Data.Data`
  as `[]byte` and adjust the jetstream/ds envelope (de)serialization to treat it as opaque
  bytes (e.g. base64 or a bytes column), then extend the codec round-trip tests across all
  four backends. Until then, CBOR should be used only with the kurrent and sqlite stores.
- **`restatedev/sdk-go` test harness vs `testcontainers-go` v0.42.** The SDK's own
  `github.com/restatedev/sdk-go/testing` helper (v0.24.0) does not compile against the repo's
  pinned `testcontainers-go` v0.42 (it calls the removed `nat.Port.Int()`; v0.42 returns
  `network.Port`). The `connectors/werestate` integration test therefore drives the SDK's real
  `server` entrypoint through a hand-rolled testcontainers harness. When the SDK adopts the
  v0.42+ `network.Port` API, replace the bespoke harness with the SDK's `testing` helper. This
  is the "known compatibility problem" exception noted in [ADR-0004](adr/0004-restate-go-sdk.md).
- **Align `wee-events.rs` to the tightened identity charset.** Feature 07 restricts
  `AggregateId` parts to RFC 3986 unreserved characters ([ADR-0008](adr/0008-aggregate-identity.md));
  Rust's `FromStr` still accepts colon-bearing keys and any charset. Go is a strict
  subset, so Go-written identities are Rust-valid, but a Rust-written identity outside
  the charset fails Go's parser (loudly). Upstream the same validation to
  `crates/wee-events/src/id.rs`.

## Decisions

Architectural decisions are recorded as ADRs under [`adr/`](adr/README.md) and
referenced from the feature documents (a decision lives in one place and is superseded,
not edited). Current log:

| # | Decision | Status |
|---|---|---|
| 0001 | JSON is the default event encoding | Superseded by 0007 — removed |
| [0002](adr/0002-cbor-library-fxamacker.md) | Use `fxamacker/cbor/v2` for CBOR | Accepted |
| [0003](adr/0003-sqlite-driver-libsql.md) | Use `go-libsql` for the SQLite/libSQL store | Accepted |
| [0004](adr/0004-restate-go-sdk.md) | Use the Restate Go SDK; wire dispatch manually | Accepted |
| [0005](adr/0005-rejection-error-modeling.md) | Model domain rejections via `error` + `errors.As` | Accepted |
| [0006](adr/0006-lint-enforcement.md) | Enforce resource-lifecycle principles via golangci-lint | Accepted |
| [0007](adr/0007-explicit-event-encoding.md) | Event encoding is an explicit constructor argument | Accepted |
| [0008](adr/0008-aggregate-identity.md) | Aggregate identity: canonical `type:key` form and validated construction | Accepted |
| [0009](adr/0009-property-based-testing-rapid.md) | Use `pgregory.net/rapid` for property-based conformance testing | Accepted |

## Reference

Each feature document is an **epic**: end-to-end user stories with embedded EARS
requirements and ADR cross-references — see [`features/README.md`](features/README.md)
for the model, the full gap table, file-ownership matrix, and source-comparison basis.
[`features/01-cbor-codec.md`](features/01-cbor-codec.md) is the worked exemplar.
