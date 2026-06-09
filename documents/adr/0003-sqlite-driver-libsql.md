# ADR-0003 — Use `go-libsql` for the SQLite/libSQL store

- **Status:** Accepted
- **Relates to:** [features/02-sqlite-turso-store.md](../features/02-sqlite-turso-store.md)

## Context

Feature 02 adds a SQLite/libSQL event-store backend that must reach the same three
targets the `wee-events.rs` sibling supports: an **in-memory** database, a **local
file**, and a **remote** Turso/sqld database. The target set is the contract — a single
constructor option set is expected to select among all three, and the conformance suite
(Feature 04) exercises in-memory and local-file backings, including shared-backing pairs.

No single pure-Go driver covers all three targets. The Go driver landscape splits along
the cgo boundary:

- `github.com/tursodatabase/go-libsql` — the official libSQL driver. Covers **embedded**
  (in-memory and local file) **and remote** sqld/Turso in one driver, but links the libSQL
  C library and therefore **requires cgo**.
- `modernc.org/sqlite` — a pure-Go SQLite transpilation. Covers **local file and
  in-memory only**; it has no libSQL remote transport.
- `github.com/tursodatabase/libsql-client-go` — a pure-Go libSQL client. Covers **remote
  sqld/Turso only**; it cannot open a local file or an embedded in-memory database.

So full target parity is reachable two ways: one driver with cgo, or two pure-Go drivers
behind a shared interface — `modernc.org/sqlite` for local/in-memory and
`libsql-client-go` for remote. The two-driver split removes the cgo dependency but doubles
the connection-management surface and means local and remote paths exercise different SQL
engines, so behaviour parity between them is no longer guaranteed by construction and must
be held by the conformance suite alone.

## Decision

The framework will use `github.com/tursodatabase/go-libsql` as the single driver for the
SQLite/libSQL store, accepting the cgo build dependency, because one driver across all
three targets keeps the store single-responsibility (one connection model, one SQL engine)
and gives the strongest behaviour parity between local and remote.

The cgo build dependency is **accepted**, on the explicit condition that cgo stays confined
to the `stores/sqlite` package: the rest of the framework remains cgo-free and buildable with
`CGO_ENABLED=0`. The pure-Go split is retained below as the documented fallback should a
future build target rule cgo out.

This choice is expected to be **revisited once Turso Database — the Rust engine behind
`turso.tech/database/tursogo` — reaches GA.** That engine is BETA today (its repository warns
to "use caution with production data and ensure you have backups"), making it unsuitable for
the durable store of record now. At GA a single cgo-free `tursogo` driver could cover
in-memory + local-file + sync (with `libsql-client-go` for direct remote), at which point a
successor ADR would supersede this one.

## Consequences

- A single driver means one connection-management path and one SQL dialect across
  in-memory, local-file, and remote targets; the local and remote paths run the same
  engine, so conformance behaviour observed locally carries to remote with high confidence.
- The build gains a cgo dependency: a C toolchain is required to build the `stores/sqlite`
  package, cross-compilation is constrained, and `CGO_ENABLED=0` builds cannot include the
  package. The cgo cost is confined to the store package; the rest of the framework is
  unaffected.
- Turso Platform provisioning (creating remote databases via the Platform API) remains a
  later phase gated behind a build tag or option, mirroring Rust's `turso` feature; it does
  not change this driver choice.
- The chosen driver and the target matrix it enables must be recorded in the
  `stores/sqlite` package doc comment so the build dependency is discoverable at the source.
- If a future build target forbids cgo, this ADR is revisited and superseded by the
  two-driver alternative below.
- The chosen libSQL engine carries capabilities the events-only store does not use but could
  inherit later: **native vector search** (`F32_BLOB` column types and the `vector_top_k`
  DiskANN index) and **encryption at rest** (AEGIS / AES-GCM via an encryption key —
  `libsql.WithEncryption`). Encryption at rest is whole-database and transparent to the
  store, so it fits as an optional `NewStore` option for local-file and embedded-replica
  targets without breaching the codec-agnostic seam (the store still persists opaque bytes).
  It is **distinct from** the unimplemented `PublishOptions.Encrypt` flag (per-payload
  encryption); implementing database-at-rest encryption does not satisfy that flag, which
  remains a separate principle-3 wart to resolve. Vector search is a read-model/projection
  concern and stays out of scope while projections are excluded.

## Alternatives considered

- **Pure-Go split — `modernc.org/sqlite` (local + in-memory) and
  `github.com/tursodatabase/libsql-client-go` (remote), behind one interface.** This is the
  documented fallback. Rejected as the primary choice because it doubles the
  connection-management surface and runs two different SQL engines, so local and remote
  parity is no longer structural and rests entirely on the conformance suite. It is the
  correct choice only if cgo is unacceptable, and is the path this ADR would be superseded
  by in that case.
- **`modernc.org/sqlite` alone (drop remote).** Rejected: it abandons the remote
  Turso/sqld target, which is part of Feature 02's contract.
- **`github.com/tursodatabase/libsql-client-go` alone (drop local + in-memory).** Rejected:
  it abandons the in-memory and local-file targets the conformance suite requires, including
  the shared-backing file pair.
- **`turso.tech/database/tursogo` (CGO-free, purego) for local + `libsql-client-go` for
  remote.** Evaluated and deferred. This is Turso's recommended pure-Go layout and avoids cgo,
  but it runs the BETA Turso Database (Rust) engine for the local store of record and pairs it
  with the production libSQL engine for remote — two different engines *and* two different
  concurrency models (MVCC versus single-writer/WAL) behind one `EventStore`, the widest
  parity gap of the options. Revisit at Turso Database GA (see Decision).
