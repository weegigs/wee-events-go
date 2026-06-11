# ADR-0013 — SQLite store: single owner per shard, partitioned by strategy

- **Status:** Accepted
- **Relates to:** [ADR-0003](0003-sqlite-driver-libsql.md) (the go-libsql driver) · [ADR-0006](0006-lint-enforcement.md) (resource lifecycle) · [ADR-0012](0012-manual-composition-roots.md)

## Context

The SQLite store shipped as a single database (the Rust GlobalStrategy only).
The 2026-06-11 benchmark collection (documents/performance-benchmarks.md)
proved the go-libsql driver fails with `SQLITE_MISUSE` ("bad parameter or
other API misuse") whenever multiple pool connections touch one local file
concurrently: 34 of 52 local-file benchmark leaves failed — reads as well as
writes — on `BEGIN IMMEDIATE`, `PRAGMA busy_timeout`, and statement prepare.
Single-goroutine leaves passed, which is why the single-goroutine conformance
suite never observed it. The in-memory target was immune only because it pins
the pool to one connection. The port mandate also requires the full Rust
partitioning layer, absent in Go.

## Decision

The SQLite store partitions by strategy (Global, ByType, ByAggregate,
Hashed(n), PartitionBy) over five backends (in-memory, local multi-file,
sqld-default, sqld-namespaced, Turso platform), and every partition is owned
by exactly one goroutine ("shard") that holds the partition's connection and
serves all load, publish, and scan requests over a channel. Each shard's
`*sql.DB` is additionally pinned with `SetMaxOpenConns(1)`. No database is
ever touched from more than one goroutine. The model is uniform across
backends — remote servers arbitrate writes, but uniform single-owner keeps
semantics backend-independent and benchmark comparisons with wee-events.rs
honest. Sharding restores parallelism across shards.

## Consequences

The `SQLITE_MISUSE` failure is impossible by construction. Concurrency now
scales with partition count: a Hashed or ByType store serves concurrent
aggregates in different shards in parallel while each shard stays serialized.
The shard map grows monotonically; unbounded strategies (ByAggregate) trade
memory for isolation and document it. Close stops N shards and closes N
databases (ADR-0006).

Connection-level concerns are scoped by target locality. WAL journaling and
`busy_timeout` are applied only to local-file targets: they let cross-instance
readers avoid blocking on a writer holding the file lock, and are converted
with a bounded retry because the WAL conversion briefly needs an exclusive lock
that `busy_timeout` does not cover. Remote backends (sqld, Turso) manage
journaling and locking server-side and reject those PRAGMAs, so the store skips
them; `:memory:` cannot use WAL. Remote targets additionally carry their access
token into the libSQL connection string, and a newly provisioned Turso database
is awaited until its edge route is live (the platform returns a hostname before
the route propagates).

A later optimization — per-shard read pools for remote backends — is left open
and is benchmarkable via `just bench-compare`; it would require a new ADR if it
breaks the uniform model.

## Alternatives considered

- **Pin every pool to one connection, no sharding.** Fixes the defect but
  keeps the store single-writer globally and ignores the partitioning mandate.
- **Per-backend concurrency (single-owner local, pooled remote).** Buys remote
  read concurrency now at the cost of divergent semantics per backend and a
  Rust comparison that is no longer apples-to-apples.
- **Drop go-libsql.** No single pure-Go driver covers in-memory, local, and
  remote libSQL (ADR-0003); switching drivers is a larger, separate decision.
