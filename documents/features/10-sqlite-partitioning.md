# Feature 10 — SQLite partitioning layer

Ports the wee-events.rs SQLite partitioning layer (design:
docs/superpowers/specs/2026-06-11-sqlite-partitioning-design.md; concurrency
decision: ADR-0013).

## Requirements (EARS)

- SQLITE-P1.R1 — The store SHALL route each aggregate to a partition via the
  configured strategy: Global, ByType, ByAggregate, Hashed(n), or PartitionBy.
- SQLITE-P1.R2 — Hashed(n) SHALL assign buckets by 32-bit FNV-1a over
  "type:key", reproducing the wee-events.rs assignment for identical inputs.
- SQLITE-P2.R1 — The local backend SHALL store each named partition as
  "b32-<BASE32_NOPAD(name)>.db", byte-compatible with wee-events.rs.
- SQLITE-P2.R2 — When a partitioned local store is reopened, it SHALL discover
  existing partitions from the filesystem.
- SQLITE-P3.R1 — The store SHALL provision sqld namespaces and Turso platform
  databases lazily, treating an already-existing target as success.
- SQLITE-P3.R2 — The store SHALL attach a remote target's access token to the
  libSQL connection, and SHALL await a newly provisioned Turso database until
  its edge route is live before first use.
- SQLITE-P3.R3 — WAL journaling and busy_timeout SHALL be applied only to
  local-file targets; remote backends and the in-memory target SHALL skip them
  (the remote engines manage journaling and locking server-side).
- SQLITE-P4.R1 — Each partition SHALL be owned by exactly one goroutine; no
  database SHALL be accessed from more than one goroutine (ADR-0013).
- SQLITE-P4.R2 — Concurrent loads and publishes across shards SHALL complete
  without `SQLITE_MISUSE`, verified under -race.
- SQLITE-P5.R1 — EnumerateAggregates and EnumerateAggregatesByType SHALL return
  the distinct aggregate ids across all known partitions, deduplicated, and
  EnumerateAggregatesByType SHALL narrow to the requested type for every
  strategy (including ScanAll strategies).
- SQLITE-P6.R1 — The encoder SHALL remain an explicit constructor argument
  (ENCODING-S2); a nil encoder SHALL be a construction error.

## Verification

- Strategies, catalogs, and provisioners: unit and property tests
  (stores/sqlite/*_test.go).
- Single-store conformance across all five local strategies and cross-instance
  shared-backing: the standard validation and shared-backing suites
  (conformance_test.go).
- The `SQLITE_MISUSE` regression is gated by a concurrent load/publish stress
  test under -race (concurrency_test.go).
- The Turso platform path — provisioning, auth, route readiness, round-trip,
  and enumeration — is exercised end to end by an env-guarded live test
  (turso_live_test.go; TURSO_* in the mise environment).

## Coverage boundary

sqld-namespaced is verified through the named-catalog unit tests against a fake
provisioner and the named-target code path shared with Turso; a live sqld
container test is out of scope. The Turso platform path is verified end to end
by the env-guarded live test.
