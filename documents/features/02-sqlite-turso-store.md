# Feature 02 — SQLite / libSQL / Turso Event Store

- **Status:** Done · **Size:** L · **Area:** new package (`stores/sqlite/`)
- **Coordinates with:** independent (disjoint files; parallel-safe with Features 03, 04)
- **Prefix:** `SQLITE`

## Summary

Add a fourth event-store backend backed by SQLite/libSQL that satisfies the same
`we.EventStore` contract as the DynamoDB, JetStream, and Kurrent backends, and that runs
in-memory, against a local file, and against a remote Turso/sqld database through one
constructor option set. The store is **events-only**: a single `events` table, no
`documents` table and no projection sink — the projection/document-store layer carried by
the `wee-events.rs` sibling is out of scope (see [the backlog overview](README.md)).
Optimistic concurrency is enforced by a unique `(aggregate_type, aggregate_key, revision)`
index, and the store never interprets payload bytes — whatever encoding Feature 01 wrote
round-trips unchanged.

## Decisions

- [ADR-0003](../adr/0003-sqlite-driver-libsql.md) — `github.com/tursodatabase/go-libsql`
  is the recommended (Proposed) single driver for in-memory + local-file + remote parity,
  accepting cgo; the pure-Go split is the recorded fallback.

## User stories

### SQLITE-S1 — Record and load events through a SQLite-backed store

*As an application developer, I want to record and load aggregate events through a
SQLite-backed store, so that I can use the framework with a SQLite/libSQL database without
changing how the rest of the framework treats a store.*

Upholds principle 1 (single responsibility — the store persists rows and reconstructs
aggregates, nothing more) and principle 2 (explicit lifecycle — `NewStore` returns a usable
value or an error, and the store's connection is released through an explicit `Close`).

- **SQLITE-S1.R1** (ubiquitous) — The framework shall provide a `stores/sqlite` store that
  satisfies the `we.EventStore` interface (`Load` and `Publish`).
- **SQLITE-S1.R2** (event-driven) — When `Publish` is called for an aggregate, the
  framework shall, within a single transaction, insert one row per event into the `events`
  table with a generated revision, the event type, the `(aggregate_type, aggregate_key)`
  identity, the `encoding` discriminator, the payload `data` BLOB, and the
  causation/correlation metadata.
- **SQLITE-S1.R3** (event-driven) — When `Load` is called for an aggregate, the framework
  shall select that aggregate's rows ordered by `revision` ascending, reconstruct each
  `RecordedEvent` (including its `Data{Encoding, Data}` and causation/correlation metadata),
  and return an `Aggregate` carrying the last revision.
- **SQLITE-S1.R4** (state-driven) — While an aggregate has no rows, the framework shall load
  it as `InitialRevision` with no events. *(Mirrors the existing backends and the
  conformance suite's `LoadInitial`.)*
- **SQLITE-S1.R5** (unwanted) — If `NewStore` cannot open or migrate the target database,
  then the framework shall return an error from the constructor and shall not return a
  half-built store. *(Principle 2.)*

### SQLITE-S2 — Optimistic-concurrency conflicts surface as `we.RevisionConflict`

*As an application developer, I want a concurrent append that violates the expected
revision to surface as `we.RevisionConflict`, so that the caller's load→conflict→reload→retry
loop behaves identically to every other backend.*

Upholds principle 3 (state is not an error — a lost write race is a known state communicated
through the typed `we.RevisionConflict` value, not a raw driver failure).

- **SQLITE-S2.R1** (state-driven) — While `PublishOptions.ExpectedRevision` is
  `InitialRevision`, the framework shall commit the append only if the aggregate currently
  has no rows.
- **SQLITE-S2.R2** (state-driven) — While `PublishOptions.ExpectedRevision` names a specific
  revision, the framework shall commit the append only if that revision is the aggregate's
  current last revision.
- **SQLITE-S2.R3** (unwanted) — If an append violates the unique
  `(aggregate_type, aggregate_key, revision)` index, then the framework shall map the SQLite
  constraint error to `we.RevisionConflict` and shall not surface a raw driver error.
- **SQLITE-S2.R4** (unwanted) — If `PublishOptions.ExpectedRevision` does not match the
  aggregate's current last revision, then the framework shall return `we.RevisionConflict`
  and shall not persist any of the events in the batch. *(Transactional all-or-nothing.)*
- **SQLITE-S2.R5** (unwanted) — If a conflict occurs, then the framework shall not retry the
  append inside the store; retry is the caller's responsibility (exercised at the caller
  level by the conformance suite — see [Feature 04](04-storage-verification-tests.md)).

### SQLITE-S3 — Run against in-memory, local-file, and remote targets

*As an operator, I want the same store to run in-memory for tests, against a local file for
single-node deployments, and against a remote Turso/sqld database for hosted deployments, so
that one backend covers development through production without a code change.*

Upholds principle 2 (explicit lifecycle — each target acquires a connection that the store
owns and releases through `Close`).

- **SQLITE-S3.R1** (ubiquitous) — The framework shall select an in-memory, local-file, or
  remote Turso/sqld target through one constructor option set.
- **SQLITE-S3.R2** (event-driven) — When the store is constructed for any supported target,
  the framework shall create or migrate the single `events` table so that `Load` and
  `Publish` behave identically across targets.
- **SQLITE-S3.R3** (state-driven) — While two store instances are backed by the same local
  file, the framework shall have each instance observe events the other has committed.
  *(Shared-backing parity — see [Feature 04](04-storage-verification-tests.md).)*
- **SQLITE-S3.R4** (ubiquitous) — The framework shall record the chosen driver and the
  resulting target matrix in the `stores/sqlite` package doc comment. *(See ADR-0003.)*
- **SQLITE-S3.R5** (optional feature) — Where Turso Platform provisioning is included, the
  framework shall create remote databases via the Platform API behind a build tag or option,
  mirroring Rust's `turso` feature. *(Later phase; not required by the initial deliverable.)*

### SQLITE-S4 — Codec-agnostic storage

*As a framework maintainer, I want the store to persist and return payload bytes verbatim,
so that any encoding written through Feature 01 round-trips unchanged and the wire format
stays the codec layer's responsibility, not the store's.*

Upholds principle 1 (single responsibility — stores persist bytes and never interpret
payloads; this is exactly the seam Feature 01's codec layer protects).

- **SQLITE-S4.R1** (ubiquitous) — The framework shall store the payload as a `data` BLOB and
  the `Data.Encoding` string in the `encoding` column, treating the payload as opaque bytes.
- **SQLITE-S4.R2** (event-driven) — When an event is loaded, the framework shall return the
  `data` bytes and `encoding` string exactly as written, performing no deserialization of
  the payload.
- **SQLITE-S4.R3** (unwanted) — If a payload is written with an `encoding` the store does not
  recognise, then the framework shall still persist and return it unchanged and shall not
  reject or rewrite it. *(The store does not own the encoding vocabulary — Feature 01 does.)*

## Implementation notes

### Current Go state

`we.EventStore` is the contract every backend satisfies (`we/event-store.go`):

```go
type EventStore interface {
	Load(ctx context.Context, id AggregateId) (Aggregate, error)
	Publish(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) error
}

var RevisionConflict = errors.New("revision-conflict")
```

`PublishOptions` carries `ExpectedRevision`, correlation/causation metadata, and the
(currently unimplemented) `Encrypt` flag. Existing backends — `stores/ds` (DynamoDB),
`stores/jetstream` (NATS), `stores/kurrent` (KurrentDB) — are the reference for how a
backend maps the contract onto a transport and surfaces `RevisionConflict`.

### Rust reference (port origin)

`crates/wee-events-sqlite/src/`:

- `database.rs` — schema and connection/migration setup.
- `event_store/store.rs` — the `EventStore` impl (`load` / `publish`).
- `event_store/strategies/*` — partition strategies (`global`, `by_type`,
  `by_aggregate`, `hashed`, `partition_by`).
- `event_store/backends/*` — target resolution (`memory`, `local`, `remote`,
  `sqld_default`, `sqld_namespaced`, `turso`).
- `event_store/turso_platform/*` — Turso Platform API provisioning (behind the `turso`
  feature).

The Go port needs only the `events` table — the `documents` table backs the excluded
projection layer:

```sql
CREATE TABLE events (
    event_id        TEXT NOT NULL CHECK(length(event_id) = 26),
    aggregate_type  TEXT NOT NULL,
    aggregate_key   TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    revision        TEXT NOT NULL CHECK(length(revision) = 26),
    causation_id    TEXT,
    correlation_id  TEXT,
    encoding        TEXT NOT NULL,
    data            BLOB NOT NULL,
    PRIMARY KEY (event_id)
);
CREATE UNIQUE INDEX idx_events_aggregate
    ON events (aggregate_type, aggregate_key, revision);
```

The unique index on `(aggregate_type, aggregate_key, revision)` is the optimistic
concurrency mechanism: a conflicting append violates it (SQLITE-S2.R3).

### Go target

New package `stores/sqlite`:

- `NewStore(...)` — pointer-returning constructor per conventions — implementing
  `we.EventStore`; returns a usable store or an error, never a half-built object
  (SQLITE-S1.R5).
- **Schema:** the single `events` table above. `data` is a `BLOB` holding the raw encoded
  payload; the store never deserializes it (codec-agnostic — see
  [Feature 01](01-cbor-codec.md), satisfies SQLITE-S4). The `encoding` column stores the
  `Data.Encoding` string.
- **`Load`:** select rows for `(aggregate_type, aggregate_key)` ordered by `revision`
  ascending, reconstruct `RecordedEvent`s (including `Data{Encoding, Data}` and
  causation/correlation metadata), and return an `Aggregate` with the last revision; an
  aggregate with no rows loads as `InitialRevision` with no events (SQLITE-S1.R3,
  SQLITE-S1.R4).
- **`Publish`:** within a transaction, insert one row per event with generated revisions,
  honouring `PublishOptions.ExpectedRevision` (SQLITE-S2.R1, SQLITE-S2.R2). Map the SQLite
  unique-index violation precisely to `we.RevisionConflict`; do not retry inside the store
  (SQLITE-S2.R3, SQLITE-S2.R5).
- **Partitioning:** start with the `GlobalStrategy` equivalent (one database for all
  aggregates). `hashed` / `by_type` / `by_aggregate` partitioning is a follow-on phase; the
  initial deliverable is a single-database store.
- **Targets:** in-memory, local-file, and remote Turso/sqld URLs through one constructor
  option set (SQLITE-S3.R1, SQLITE-S3.R2). Turso Platform provisioning is a later phase,
  gated behind a build tag or option, mirroring Rust's `turso` feature (SQLITE-S3.R5).
- **Driver:** see [ADR-0003](../adr/0003-sqlite-driver-libsql.md) — recommended
  `github.com/tursodatabase/go-libsql` (cgo) for full parity; pure-Go split
  (`modernc.org/sqlite` + `libsql-client-go`) is the recorded fallback. Record the chosen
  matrix and rationale in the package doc comment (SQLITE-S3.R4).

## Verification

| Requirement | Test |
|---|---|
| SQLITE-S1.R1 | Assert `*stores/sqlite.Store` satisfies `we.EventStore` (compile-time assignment plus suite run). |
| SQLITE-S1.R2, SQLITE-S1.R3 | Run the conformance suite (Feature 04) against in-memory and local-file backings; record events and reload, asserting type, encoding, data, metadata, and last revision. |
| SQLITE-S1.R4 | `LoadInitial`: load an unknown aggregate; assert `InitialRevision`, no events. |
| SQLITE-S1.R5 | Construct against an unwritable/invalid target; assert `NewStore` returns an error and no store value. |
| SQLITE-S2.R1, SQLITE-S2.R2 | Publish with `InitialRevision` against an empty aggregate and with a matching specific revision; assert both commit. |
| SQLITE-S2.R3 | Force a unique-index violation directly; assert it surfaces as `we.RevisionConflict`, not a raw driver error. |
| SQLITE-S2.R4 | Publish a multi-event batch with a stale `ExpectedRevision`; assert `we.RevisionConflict` and that no row from the batch persisted. |
| SQLITE-S2.R5 | Drive the load→conflict→reload→retry loop at the caller level (Feature 04); assert the store itself performs no internal retry. |
| SQLITE-S3.R1, SQLITE-S3.R2 | Construct the store for in-memory and local-file targets through the one option set; assert identical `Load`/`Publish` behaviour. |
| SQLITE-S3.R3 | Shared-backing pair: two instances over the same file; assert one observes the other's committed events. |
| SQLITE-S3.R4 | Assert the package doc comment records the chosen driver and target matrix. |
| SQLITE-S4.R1, SQLITE-S4.R2 | Write a payload with a given encoding and BLOB; reload; assert bytes and encoding returned verbatim. |
| SQLITE-S4.R3 | Write a payload with an arbitrary unknown encoding string; reload; assert it is stored and returned unchanged, neither rejected nor rewritten. |

Integration against a Turso/sqld container (SQLITE-S3.R5) is a later phase, gated like the
existing `just test-integration` Docker-backed tests. Verification is by running these tests
(`just test`), not by assertion.
