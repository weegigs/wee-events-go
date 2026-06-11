# SQLite Partitioning Layer — Design

## Decision

The `stores/sqlite` store gains the full partitioning layer from
wee-events.rs: five partition strategies (`Global`, `ByType`, `ByAggregate`,
`Hashed(n)`, `PartitionBy(fn)`), three catalog implementations covering five
backends (in-memory, local multi-file, sqld-default, sqld-namespaced, Turso
platform), aggregate enumeration, and a uniform single-owner-per-shard
concurrency model: one goroutine owns each shard's connection, fed by a
request channel. The existing single-database `Store` API is replaced — the
old behaviour survives as `Global()` over the local backend. The document
store and projections subsystems are explicitly excluded from this effort.

The concurrency model is load-bearing, not stylistic: the 2026-06-11
benchmark collection proved go-libsql fails with `SQLITE_MISUSE` whenever
multiple pool connections touch one local file concurrently (34 of 52
local-file leaves failed — reads as well as writes), while the
single-connection in-memory target passed everything. One owner per shard
makes the failure impossible by construction; sharding restores parallelism
across shards. The model applies uniformly to remote backends to match the
Rust reference (one connection per partition behind a mutex), keeping
cross-implementation benchmarks comparable.

## Constraints

- Full fidelity to wee-events.rs: same strategy names and semantics, same
  FNV-1a bucket assignment, same `b32-<BASE32_NOPAD(name)>.db` local layout,
  same `_wee_events_partition_metadata` table (schema v2). A partition
  directory written by either implementation is readable by the other.
- Turso is the key backend and ships in this effort, not as a follow-on.
- Encoder remains an explicit constructor argument (ENCODING-S2, ADR-0007/0011).
- Identity grammar v2 governs aggregate ids (ADR-0010); partition names for
  `ByType` are therefore lowercase kebab, but base32 encoding is retained for
  layout compatibility.
- Lifecycle symmetry (ADR-0006): N shards opened ⇒ N closed; construction
  fails whole, never half-built (SQLITE-S1.R5).
- Manual constructor wiring, no DI framework (ADR-0012).
- No lint suppressions.

## Architecture

### Store core and shard actors

```go
type Store struct {
    strategy PartitionStrategy
    catalog  PartitionCatalog
    encoder  we.Encoder
    mu       sync.Mutex                // guards shards, known, closed
    shards   map[Partition]*shard
    known    map[Partition]struct{}    // every partition touched (mirrors Rust known_partitions)
    closed   bool
}
```

`Partition` is a small comparable value (name + default marker, mirroring
Rust `PartitionName::Default | Named`) usable as a map key.

A `shard` owns its database exclusively: a dedicated `*sql.DB` opened from
the catalog target and pinned with `SetMaxOpenConns(1)`, a typed request
channel, and one goroutine consuming it. Requests (`load`, `publish`,
`enumerate`) carry the caller's `context.Context` and a reply channel;
callers select on reply vs `ctx.Done()`, so cancellation works while queued.
The `maxOpen=1` pin is deliberate redundancy beneath the actor: the invariant
holds even if a future bug routes around the channel.

Lifecycle mirrors Rust:

- `Publish` → `ensureShard`: under `mu`, hit the map or call
  `catalog.EnsureTarget` (idempotent provisioning), open, migrate schema,
  write the logical name via the prepare hook, spawn the actor.
- `Load` → non-creating path via `ExistingTarget`; absent shard ⇒ empty
  aggregate, nothing provisioned (state, not an error).
- The shard map grows monotonically; unbounded strategies (`ByAggregate`)
  document the trade-off, as Rust does.
- `Close()` stops every actor and closes every DB; subsequent operations
  return a closed-store error checked under `mu` before any channel send.

Inside the actor, publish semantics are unchanged from the current store:
`BEGIN IMMEDIATE`, conditional insert enforcing the revision guard, bounded
`SQLITE_BUSY` retries, `we.RevisionConflict` on guard failure — never
retried (SQLITE-S2.R5).

### Strategies

```go
type PartitionStrategy interface {
    PartitionFor(id we.AggregateId) Partition
    PartitionName(p Partition) string                  // stable; names files/namespaces
    PartitionFromName(name string) (Partition, error)  // round-trip for discovery
    ReadPlan(p Partition) ReadPlan                      // ScanAll | ScanType | Direct | Skip
}
```

| Strategy | Partition key | Name | Bounded |
|---|---|---|---|
| `Global()` | constant | default marker | yes (1) |
| `ByType()` | aggregate type | the type string | yes (distinct types) |
| `ByAggregate()` | full id | `type:key` | no |
| `Hashed(n)` | FNV-1a(`type:key`) mod n | `bucket-<i>` | yes (n) |
| `PartitionBy(fn)` | `fn(id) string` | fn output | user-defined |

FNV-1a parameters (offset `0x811c9dc5`, prime `0x01000193`) are pinned by
test vectors copied from the Rust suite: the same aggregate lands in the same
bucket in both implementations.

Marker sub-interfaces — `LocalStrategy`, `SingleTargetStrategy`,
`NamingStrategy` — encode legal strategy×backend pairs in constructor
signatures, replacing Rust's type-state builder:

```go
NewStore(ctx, encoder, Local(dir, ByType()))          // local multi-file
NewStore(ctx, encoder, InMemory(Global()))            // single target
NewStore(ctx, encoder, SqldDefault(cfg, Global()))
NewStore(ctx, encoder, SqldNamespaced(cfg, Hashed(8)))
NewStore(ctx, encoder, Turso(cfg, ByType()))
```

### Catalogs and provisioners

```go
type PartitionCatalog interface {
    EnsureTarget(ctx context.Context, p Partition) (Target, error)         // idempotent
    ExistingTarget(ctx context.Context, p Partition) (Target, bool, error) // never creates
    Partitions(ctx context.Context) ([]Partition, error)                   // discovery
    PrepareShard(ctx context.Context, p Partition, db *sql.DB) error       // metadata hook
}
```

`Target` carries DSN, auth token, and namespace; auth tokens flow through the
existing `redactToken` discipline so provisioning errors cannot leak
credentials.

- **`localCatalog`** — `Global` maps to the root path as a single `.db`;
  named strategies map to `b32-<BASE32_NOPAD(name)>.db` files in a directory.
  Discovery scans `*.db`, decodes, round-trips through `PartitionFromName`.
- **`singleTargetCatalog`** — in-memory and sqld-default; every partition maps
  to the one target; `Partitions()` returns empty (no enumeration), matching
  Rust.
- **`namedTargetCatalog`** — sqld-namespaced and Turso, parameterized by:

```go
type Provisioner interface {
    EnsureTarget(ctx context.Context, name PartitionName) (Target, error)
    ExistingTarget(ctx context.Context, name PartitionName) (Target, bool, error)
    NamedTargets(ctx context.Context) ([]NamedTarget, error)
}
```

  Its `Partitions()` performs the Rust three-step discovery: list named
  targets, read each shard's `_wee_events_partition_metadata.logical_name`,
  fall back to strategy-specific discovery (`ByType` queries
  `SELECT DISTINCT aggregate_type`) for shards predating metadata.

Provisioner implementations:

- **sqld** — admin API `POST /v1/namespaces/{name}/create`, bounded retry.
- **Turso platform** — org/group/prefix/API-token configuration; database
  names `{prefix}-{sanitized}`; create-database with the already-exists race
  resolved by re-fetch; name/target cache; discovery via the platform API
  plus per-database metadata. The platform API client sits behind a small
  interface: a fake drives conformance tests; an env-guarded live test keyed
  on `TURSO_API_TOKEN` exercises the real API.

Publish retains Rust's lazy-create recovery: on a namespace-missing error,
re-ensure and retry, bounded at 30 attempts × 1 s, context-aware.

### Enumeration

On `*sqlite.Store`, not `we.EventStore` (other backends gain no new
obligations):

```go
func (s *Store) EnumerateAggregates(ctx context.Context) ([]we.AggregateId, error)
func (s *Store) EnumerateAggregatesByType(ctx context.Context, t string) ([]we.AggregateId, error)
```

Both union `catalog.Partitions()` with `known`, apply each partition's
`ReadPlan` (`Direct` answers from the name; `ScanType` narrows; `Skip`
prunes), execute scans through the shard actors, dedupe, sort.

## Error handling

- Construction fails whole; no half-built store.
- Closed store: operations return a closed-store error; the flag is checked
  under `mu` before any channel send (no send-on-closed-channel path).
- Provisioning errors wrap the partition name; tokens redacted.
- Empty/never-provisioned partitions load as empty aggregates — state, not
  error.
- Revision conflicts surface as `we.RevisionConflict`, never retried.

## Testing

| Layer | Coverage |
|---|---|
| Unit (rapid) | strategy name round-trips over grammar v2 identity generators; FNV-1a pinned to Rust test vectors; base32 encode/decode; local-layout discovery rescans |
| Conformance | validation suite per strategy×backend: local×{all five}, in-memory×global, sqld-default×global, sqld-namespaced×{type, aggregate, hashed, partitionBy} (testcontainers), Turso×named (fake provisioner); shared-backing suite on local layouts |
| Concurrency | `-race` stress test (N goroutines, loads+publishes across shards) in `just test`; `just bench` must produce a fully populated SQLite-file column — zero `✗` — as the regression gate for the go-libsql defect |
| Live Turso | one env-guarded test (skip unless the `TURSO_*` set below is present): provision a prefixed database, round-trip events, enumerate, delete |
| Benchmarks | per-strategy entry points mirroring the Rust suite's instantiations (`BenchmarkSqliteLocalByType`, `BenchmarkSqliteLocalHashed`, …); results recorded in `documents/performance-benchmarks.md` in a follow-up collection |

## Documentation obligations

- ADR: single-owner-per-shard concurrency for the SQLite store — records the
  `SQLITE_MISUSE` evidence, the channel-actor decision, uniform application
  across backends; amends the operational picture around ADR-0003 without
  superseding it.
- Feature document under `documents/features/` with EARS requirements.
- Conformance and benchmark entry points updated to the new constructors.

## Out of scope

- Document store (`document_store.rs`) and projections (`projections.rs`) —
  explicitly excluded by the owner; separate effort.
- Per-shard read pools for remote backends — a later, benchmarkable
  optimization; uniform single-owner ships first.
- Restate effect routing, macro-equivalent codegen — tracked by the gap
  audit, separate efforts.

## Live-test configuration

The mise environment provides the full Turso configuration; the live test
reads it directly and skips when any variable is absent:

| Variable | Role |
|---|---|
| `TURSO_ORG` | platform organisation |
| `TURSO_GROUP` | replication group databases are created in |
| `TURSO_DB_PREFIX` | database-name prefix (`{prefix}-{sanitized}`) |
| `TURSO_API_TOKEN` | platform API authentication (create/list/delete) |
| `TURSO_GROUP_TOKEN` | database access token for the provisioned shards |
