# SQLite Partitioning Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Port the wee-events.rs SQLite partitioning layer to Go — five partition strategies, five backends (in-memory, local multi-file, sqld-default, sqld-namespaced, Turso platform), aggregate enumeration, and a uniform single-owner-per-shard concurrency model that resolves the go-libsql `SQLITE_MISUSE` concurrency defect.

**Architecture:** A `PartitionStrategy` maps an `AggregateId` to a `Partition`; a `PartitionCatalog` maps a `Partition` to a storage `Target`; each partition is owned by one `shard` goroutine that holds its `*sql.DB` (pinned to one connection) and serves load/publish/enumerate requests over a channel. The store routes each operation to its shard, provisioning lazily. The existing single-database `Store` API is replaced; `Global()` over the local backend reproduces today's behaviour.

**Tech Stack:** Go 1.26, `github.com/tursodatabase/go-libsql` (driver), `encoding/base32` (local layout), `hash/fnv` semantics (hand-rolled to match Rust), testify + `pgregory.net/rapid` (tests), testcontainers (sqld conformance), Turso Platform HTTP API.

---

## Reference Material

- **Spec (source of truth):** `docs/superpowers/specs/2026-06-11-sqlite-partitioning-design.md`
- **Current store (semantics that carry over):** `stores/sqlite/store.go` — the publish transaction (`publishOnce`: `BEGIN IMMEDIATE`, `currentSequence`, `checkExpectedRevision`, conditional insert, `isUniqueViolation` → `we.RevisionConflict`, `busyRetries`), `Load` query, `encodeEvents`, `redactToken`, `applyPragmas`/`applyBusyTimeout`, `revisionForSequence`/`sequenceForRevision`, `newEventID`, `timestampFromEventID`. These move into the shard actor unchanged in behaviour.
- **Rust reference:** `…/wee-events.rs/crates/wee-events-sqlite/src/event_store/` — `partitioning.rs`, `types.rs`, `strategies/*.rs`, `backends/*.rs`, `store.rs`, `turso_platform/mod.rs`, `database.rs`.
- **Suites to keep green:** `we/event-store-validation-suite.go` (`NewEventStoreValidationSuite(ctx, store).Run(t)`, `NewSharedBackingSuite(ctx, a, b).Run(t)`), `we/event-store-benchmark-suite.go` (`NewEventStoreBenchmarkSuite(ctx, store).Run(b)`).

## House Rules (apply to every task)

- Commits use **jj**: `jj split <files...> -m "<past-tense message>"`. Never `git`, never `jj describe` without `-r`.
- All Go commands run under mise: `mise exec -- go test ./stores/sqlite/...`, etc.
- Constructors: `New*` returns a pointer, `Make*` returns a value.
- Comments use objective voice (no "we"/"I"); annotate non-obvious constraints only.
- **No lint suppressions** (`//nolint`, `//lint:ignore` forbidden). Discard errors only with explicit `_ =` plus a comment.
- `gofmt -s` clean; `golangci-lint run ./stores/sqlite/...` reports 0 issues at every commit.
- Tests use testify (`require`/`assert`); property tests use `pgregory.net/rapid`.

## File Structure

New files under `stores/sqlite/` (package `sqlite`, flat — matching the existing layout):

| File | Responsibility |
|---|---|
| `partition.go` | `Partition` value type, `ReadPlan` type and variants |
| `strategy.go` | `PartitionStrategy` interface + marker interfaces; `Global`, `ByType`, `ByAggregate`, `PartitionBy` |
| `strategy-hashed.go` | `Hashed(n)` strategy + the FNV-1a function |
| `catalog.go` | `PartitionCatalog` interface, `Target` type, `Backend` type |
| `catalog-local.go` | `localCatalog` (single-file + b32 multi-file layouts, discovery) |
| `catalog-single.go` | `singleTargetCatalog` (in-memory, sqld-default) |
| `catalog-named.go` | `namedTargetCatalog` + `Provisioner` interface |
| `metadata.go` | schema v2 DDL, `ensurePartitionName` |
| `shard.go` | `shard` actor: owns `*sql.DB`, request channel, load/publish/enumerate ops |
| `store.go` | **rewritten** `Store`: routing, `ensureShard`, `Close`, enumeration, constructors |
| `provisioner-sqld.go` | sqld admin-API provisioner |
| `turso-client.go` | Turso Platform API client interface + HTTP impl + fake |
| `turso-provisioner.go` | Turso `Provisioner` over the client |

Modified: `stores/sqlite/store_test.go`, `stores/sqlite/store_bench_test.go`. Deleted from old `store.go`: `InMemory()`, `LocalFile()`, `Remote()`, `BusyTimeout()` Options and the old `config`/`NewStore` shape (Task 11). New docs: `documents/adr/0013-sqlite-single-owner-shards.md`, `documents/features/10-sqlite-partitioning.md`.

---

## Task 1: Partition value type and ReadPlan

**Files:**
- Create: `stores/sqlite/partition.go`
- Test: `stores/sqlite/partition_test.go`

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPartitionDefaultIsDistinctFromNamed(t *testing.T) {
	def := DefaultPartition()
	named := MakePartition("bucket-3")

	assert.True(t, def.IsDefault())
	assert.False(t, named.IsDefault())
	assert.Equal(t, "bucket-3", named.Name())
	assert.NotEqual(t, def, named)
}

func TestPartitionsAreMapKeys(t *testing.T) {
	m := map[Partition]int{}
	m[MakePartition("a")] = 1
	m[MakePartition("a")] = 2
	m[DefaultPartition()] = 3

	assert.Len(t, m, 2)
	assert.Equal(t, 2, m[MakePartition("a")])
}

func TestReadPlanVariants(t *testing.T) {
	assert.Equal(t, readScanAll, ScanAll().kind)
	assert.Equal(t, "order", ScanType("order").aggregateType)
	assert.Equal(t, "order:1", Direct(we_AggregateId("order", "1")).id.Encode().String())
	assert.Equal(t, readSkip, Skip().kind)
}
```

Add a helper at the bottom of the test file:

```go
func we_AggregateId(t, k string) we.AggregateId { return we.AggregateId{Type: t, Key: k} }
```

and import `"github.com/weegigs/wee-events-go/we"`.

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestPartition -v`
Expected: FAIL — `undefined: DefaultPartition` etc.

- [x] **Step 3: Write the implementation**

```go
package sqlite

import "github.com/weegigs/wee-events-go/we"

// Partition is a logical storage partition. It is a comparable value so it can
// key the store's shard map. The default partition is the single-database case
// (GlobalStrategy); named partitions carry a stable, strategy-derived name used
// for files and namespaces. Mirrors Rust PartitionName::Default | Named.
type Partition struct {
	name      string
	isDefault bool
}

// DefaultPartition is the singleton partition used by single-database strategies.
func DefaultPartition() Partition { return Partition{isDefault: true} }

// MakePartition builds a named partition.
func MakePartition(name string) Partition { return Partition{name: name} }

func (p Partition) Name() string    { return p.name }
func (p Partition) IsDefault() bool { return p.isDefault }

type readKind int

const (
	readScanAll readKind = iota
	readScanType
	readDirect
	readSkip
)

// ReadPlan tells enumeration how to harvest aggregate ids from a partition:
// ScanAll reads every aggregate, ScanType narrows to one aggregate type, Direct
// names a single aggregate without a query, and Skip omits the partition.
type ReadPlan struct {
	kind          readKind
	aggregateType string
	id            we.AggregateId
}

func ScanAll() ReadPlan                  { return ReadPlan{kind: readScanAll} }
func ScanType(t string) ReadPlan         { return ReadPlan{kind: readScanType, aggregateType: t} }
func Direct(id we.AggregateId) ReadPlan  { return ReadPlan{kind: readDirect, id: id} }
func Skip() ReadPlan                     { return ReadPlan{kind: readSkip} }
```

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run "TestPartition|TestReadPlan" -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/partition.go stores/sqlite/partition_test.go -m "Added the Partition value type and ReadPlan variants for the SQLite partitioning layer"
```

---

## Task 2: Strategy interface and the four non-hashed strategies

**Files:**
- Create: `stores/sqlite/strategy.go`
- Test: `stores/sqlite/strategy_test.go`

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/weegigs/wee-events-go/we"
)

func TestGlobalStrategyRoutesEverythingToDefault(t *testing.T) {
	s := Global()
	p := s.PartitionFor(we.AggregateId{Type: "order", Key: "1"})
	assert.True(t, p.IsDefault())
	assert.Equal(t, ScanAll().kind, s.ReadPlan(p).kind)
}

func TestByTypePartitionsByAggregateType(t *testing.T) {
	s := ByType()
	p := s.PartitionFor(we.AggregateId{Type: "order", Key: "1"})
	assert.Equal(t, "order", p.Name())

	plan := s.ReadPlan(p)
	assert.Equal(t, ScanType("order").kind, plan.kind)
	assert.Equal(t, "order", plan.aggregateType)

	back, err := s.PartitionFromName("order")
	require.NoError(t, err)
	assert.Equal(t, p, back)
}

func TestByAggregateRoundTrip(t *testing.T) {
	s := ByAggregate()
	id := we.AggregateId{Type: "order", Key: "abc"}
	p := s.PartitionFor(id)
	assert.Equal(t, "order:abc", p.Name())

	plan := s.ReadPlan(p)
	assert.Equal(t, Direct(id).kind, plan.kind)
	assert.Equal(t, id, plan.id)

	back, err := s.PartitionFromName("order:abc")
	require.NoError(t, err)
	assert.Equal(t, p, back)
}

func TestPartitionByUsesClosure(t *testing.T) {
	s := PartitionBy(func(id we.AggregateId) string { return id.Type + "-shard" })
	p := s.PartitionFor(we.AggregateId{Type: "order", Key: "1"})
	assert.Equal(t, "order-shard", p.Name())
}

// Name round-trips for every grammar-v2 identity (ADR-0010).
func TestByTypeNameRoundTripsProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		typ := we.IdentityTypeGen().Draw(rt, "type")
		s := ByType()
		p := s.PartitionFor(we.AggregateId{Type: typ, Key: "x"})
		back, err := s.PartitionFromName(s.PartitionName(p))
		require.NoError(rt, err)
		assert.Equal(rt, p, back)
	})
}
```

(Delete the stray empty `TestByAggregatePartitionsPerAggregate` declaration if a linter objects; it is a placeholder — replace with nothing.)

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run "Strategy|ByType|ByAggregate|PartitionBy" -v`
Expected: FAIL — `undefined: Global`.

- [x] **Step 3: Write the implementation**

```go
package sqlite

import (
	"fmt"
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

// PartitionStrategy derives a partition from an aggregate id, names it stably
// for file/namespace layout, recovers it from that name during discovery, and
// supplies the read plan enumeration uses. Mirrors the Rust PartitionStrategy.
type PartitionStrategy interface {
	PartitionFor(id we.AggregateId) Partition
	PartitionName(p Partition) string
	PartitionFromName(name string) (Partition, error)
	ReadPlan(p Partition) ReadPlan
}

// Marker interfaces gate which strategies are legal with which backend
// constructor, replacing the Rust type-state builder. A strategy that produces
// the default partition satisfies SingleTargetStrategy; one that produces named
// partitions satisfies NamingStrategy; LocalStrategy covers both file layouts.
type LocalStrategy interface{ PartitionStrategy }
type SingleTargetStrategy interface{ PartitionStrategy }
type NamingStrategy interface{ PartitionStrategy }

// global routes every aggregate to the default partition: one database.
type global struct{}

func Global() *global { return &global{} }

func (g *global) PartitionFor(we.AggregateId) Partition { return DefaultPartition() }
func (g *global) PartitionName(Partition) string        { return "" }
func (g *global) PartitionFromName(string) (Partition, error) {
	return DefaultPartition(), nil
}
func (g *global) ReadPlan(Partition) ReadPlan { return ScanAll() }

// byType partitions by aggregate type. The type string is the partition name;
// grammar v2 guarantees it is lowercase kebab, safe for files and namespaces.
type byType struct{}

func ByType() *byType { return &byType{} }

func (b *byType) PartitionFor(id we.AggregateId) Partition { return MakePartition(id.Type) }
func (b *byType) PartitionName(p Partition) string         { return p.Name() }
func (b *byType) PartitionFromName(name string) (Partition, error) {
	if name == "" {
		return Partition{}, fmt.Errorf("sqlite: empty partition name for by-type strategy")
	}
	return MakePartition(name), nil
}
func (b *byType) ReadPlan(p Partition) ReadPlan { return ScanType(p.Name()) }

// byAggregate partitions per aggregate. The name is the encoded "type:key", so
// the read plan answers Direct without a scan.
type byAggregate struct{}

func ByAggregate() *byAggregate { return &byAggregate{} }

func (b *byAggregate) PartitionFor(id we.AggregateId) Partition {
	return MakePartition(id.Encode().String())
}
func (b *byAggregate) PartitionName(p Partition) string { return p.Name() }
func (b *byAggregate) PartitionFromName(name string) (Partition, error) {
	if _, err := we.EncodedAggregateId(name).Decode(); err != nil {
		return Partition{}, fmt.Errorf("sqlite: invalid by-aggregate partition name %q: %w", name, err)
	}
	return MakePartition(name), nil
}
func (b *byAggregate) ReadPlan(p Partition) ReadPlan {
	id, err := we.EncodedAggregateId(p.Name()).Decode()
	if err != nil {
		// A partition whose name does not decode cannot name an aggregate; it is
		// pruned from enumeration rather than failing the whole scan.
		return Skip()
	}
	return Direct(id)
}

// partitionBy routes via a caller closure. Names are whatever the closure emits;
// the read plan is the conservative ScanAll because the mapping is opaque.
type partitionBy struct {
	fn func(we.AggregateId) string
}

func PartitionBy(fn func(we.AggregateId) string) *partitionBy { return &partitionBy{fn: fn} }

func (b *partitionBy) PartitionFor(id we.AggregateId) Partition {
	return MakePartition(strings.TrimSpace(b.fn(id)))
}
func (b *partitionBy) PartitionName(p Partition) string { return p.Name() }
func (b *partitionBy) PartitionFromName(name string) (Partition, error) {
	if name == "" {
		return Partition{}, fmt.Errorf("sqlite: empty partition name for partition-by strategy")
	}
	return MakePartition(name), nil
}
func (b *partitionBy) ReadPlan(Partition) ReadPlan { return ScanAll() }
```

Verify `we.IdentityTypeGen`, `we.EncodedAggregateId`, and `(EncodedAggregateId).Decode` exist (`we/identity-gen.go`, `we/aggregate-id.go`). If `IdentityTypeGen` is named differently, adjust the property test import accordingly.

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run "Strategy|ByType|ByAggregate|PartitionBy" -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/strategy.go stores/sqlite/strategy_test.go -m "Added the partition strategy interface with global, by-type, by-aggregate, and partition-by strategies"
```

---

## Task 3: Hashed strategy with Rust-pinned FNV-1a vectors

**Files:**
- Create: `stores/sqlite/strategy-hashed.go`
- Test: `stores/sqlite/strategy-hashed_test.go`

- [x] **Step 1: Write the failing test**

The vectors below were computed from the Rust `hash_aggregate_id` (FNV-1a 32-bit, offset `0x811c9dc5`, prime `0x01000193`, over `type ++ ':' ++ key`). They pin cross-implementation bucket parity.

```go
package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func TestHashedFNVMatchesRust(t *testing.T) {
	cases := []struct {
		typ, key string
		hash     uint32
	}{
		{"order", "abc", 1743764345},
		{"order", "xyz", 2324409474},
		{"user", "kevin", 99363673},
		{"campaign", "123", 161394085},
		{"widget", "42", 1608878411},
	}
	for _, c := range cases {
		assert.Equalf(t, c.hash, fnv1aAggregate(we.AggregateId{Type: c.typ, Key: c.key}),
			"hash mismatch for %s:%s", c.typ, c.key)
	}
}

func TestHashedBucketAssignment(t *testing.T) {
	s := Hashed(8)
	// campaign:123 hashes to 161394085; 161394085 % 8 == 5.
	p := s.PartitionFor(we.AggregateId{Type: "campaign", Key: "123"})
	assert.Equal(t, "bucket-5", p.Name())
	assert.Equal(t, ScanAll().kind, s.ReadPlan(p).kind)
}

func TestHashedNameRoundTrip(t *testing.T) {
	s := Hashed(16)
	p := s.PartitionFor(we.AggregateId{Type: "order", Key: "xyz"})
	back, err := s.PartitionFromName(s.PartitionName(p))
	require.NoError(t, err)
	assert.Equal(t, p, back)
}

func TestHashedRejectsZeroBuckets(t *testing.T) {
	assert.Panics(t, func() { Hashed(0) })
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestHashed -v`
Expected: FAIL — `undefined: fnv1aAggregate`.

- [x] **Step 3: Write the implementation**

```go
package sqlite

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

const (
	fnvOffset32 uint32 = 0x811c9dc5
	fnvPrime32  uint32 = 0x01000193
)

// fnv1aAggregate hashes "type:key" with 32-bit FNV-1a. It reproduces the Rust
// hash_aggregate_id byte-for-byte so an aggregate lands in the same bucket in
// both implementations (cross-implementation layout parity).
func fnv1aAggregate(id we.AggregateId) uint32 {
	hash := fnvOffset32
	mix := func(b byte) {
		hash ^= uint32(b)
		hash *= fnvPrime32
	}
	for i := 0; i < len(id.Type); i++ {
		mix(id.Type[i])
	}
	mix(':')
	for i := 0; i < len(id.Key); i++ {
		mix(id.Key[i])
	}
	return hash
}

// hashed shards into a fixed number of buckets named "bucket-<i>".
type hashed struct {
	buckets uint32
}

// Hashed builds a bucketed strategy. A zero bucket count is a programming error
// (division by zero), reported as a panic at construction, not a deferred fault.
func Hashed(buckets uint32) *hashed {
	if buckets == 0 {
		panic("sqlite: Hashed requires a non-zero bucket count")
	}
	return &hashed{buckets: buckets}
}

func (h *hashed) PartitionFor(id we.AggregateId) Partition {
	bucket := fnv1aAggregate(id) % h.buckets
	return MakePartition(fmt.Sprintf("bucket-%d", bucket))
}
func (h *hashed) PartitionName(p Partition) string { return p.Name() }
func (h *hashed) PartitionFromName(name string) (Partition, error) {
	digits, ok := strings.CutPrefix(name, "bucket-")
	if !ok {
		return Partition{}, fmt.Errorf("sqlite: invalid hashed partition name %q", name)
	}
	if _, err := strconv.ParseUint(digits, 10, 32); err != nil {
		return Partition{}, fmt.Errorf("sqlite: invalid hashed partition name %q: %w", name, err)
	}
	return MakePartition(name), nil
}
func (h *hashed) ReadPlan(Partition) ReadPlan { return ScanAll() }
```

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run TestHashed -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/strategy-hashed.go stores/sqlite/strategy-hashed_test.go -m "Added the hashed partition strategy with FNV-1a bucketing pinned to the Rust vectors"
```

---

## Task 4: Schema v2 and partition metadata

**Files:**
- Create: `stores/sqlite/metadata.go`
- Modify: `stores/sqlite/store.go` (move the `schema` var out — see note)
- Test: `stores/sqlite/metadata_test.go`

> Note: the existing `schema` slice in `store.go` defines the `events` table and its unique index. Move that slice verbatim into `metadata.go` and extend it with the partition-metadata table, so all DDL lives in one place. Leave the rest of `store.go` untouched in this task (it is rewritten in Task 10).

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openMigrated(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open(driverName, ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, migrate(ctx, db))
	return db
}

func TestMigrateCreatesMetadataTable(t *testing.T) {
	db := openMigrated(t)
	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='_wee_events_partition_metadata'`,
	).Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "_wee_events_partition_metadata", name)
}

func TestEnsurePartitionNameIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t)

	require.NoError(t, ensurePartitionName(ctx, db, "order"))
	require.NoError(t, ensurePartitionName(ctx, db, "order"))

	got, err := readPartitionName(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, "order", got)
}

func TestEnsurePartitionNameRejectsMismatch(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t)

	require.NoError(t, ensurePartitionName(ctx, db, "order"))
	err := ensurePartitionName(ctx, db, "user")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "partition name mismatch")
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run "TestMigrate|TestEnsurePartition" -v`
Expected: FAIL — `undefined: migrate` / `undefined: ensurePartitionName`.

- [x] **Step 3: Write the implementation**

Move the existing `schema` slice from `store.go` into `metadata.go` and add the metadata table + helpers:

```go
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// schema is migrated one statement at a time: the go-libsql driver's
// ExecContext runs only the first statement of a multi-statement string, so
// each table and index MUST be a separate Exec (SQLITE-S2.R3). Schema v2 adds
// the partition-metadata table that records a shard's logical partition name.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS events (
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
);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_events_aggregate
    ON events (aggregate_type, aggregate_key, revision);`,
	`CREATE TABLE IF NOT EXISTS _wee_events_partition_metadata (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`,
}

const partitionNameKey = "logical_name"

// migrate applies every schema statement against db.
func migrate(ctx context.Context, db *sql.DB) error {
	for _, statement := range schema {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("sqlite: failed to migrate schema: %w", err)
		}
	}
	return nil
}

// ensurePartitionName records the shard's logical partition name, idempotently.
// A shard already bound to a different name is a routing error: provisioning
// returned the wrong database for this partition.
func ensurePartitionName(ctx context.Context, db *sql.DB, name string) error {
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO _wee_events_partition_metadata (key, value) VALUES (?, ?)`,
		partitionNameKey, name,
	); err != nil {
		return fmt.Errorf("sqlite: failed to record partition name: %w", err)
	}

	stored, err := readPartitionName(ctx, db)
	if err != nil {
		return err
	}
	if stored != name {
		return fmt.Errorf("sqlite: partition name mismatch: shard holds %q, routed as %q", stored, name)
	}
	return nil
}

// readPartitionName returns the shard's recorded logical name, or "" if none.
func readPartitionName(ctx context.Context, db *sql.DB) (string, error) {
	var value string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM _wee_events_partition_metadata WHERE key = ?`, partitionNameKey,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: failed to read partition name: %w", err)
	}
	return value, nil
}
```

Delete the now-duplicated `schema` var from `store.go` (the old one at the top of the file). Leave the old `Store.prepare` referencing `schema` — it still resolves to the moved var. `store.go` is rewritten in Task 10 regardless.

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run "TestMigrate|TestEnsurePartition" -v && mise exec -- go build ./stores/sqlite/`
Expected: PASS and clean build.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/metadata.go stores/sqlite/store.go stores/sqlite/metadata_test.go -m "Added schema v2 partition-metadata table and idempotent logical-name recording"
```

---

## Task 5: Target, Backend, and the catalog interface

**Files:**
- Create: `stores/sqlite/catalog.go`
- Test: `stores/sqlite/catalog_test.go`

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTargetDSNInMemory(t *testing.T) {
	tgt := Target{dsn: ":memory:"}
	assert.Equal(t, ":memory:", tgt.dsn)
	assert.Empty(t, tgt.authToken)
}

func TestTargetRedactionToken(t *testing.T) {
	tgt := Target{dsn: "libsql://db.turso.io", authToken: "secret"}
	assert.Equal(t, "secret", tgt.authToken)
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestTarget -v`
Expected: FAIL — `undefined: Target`.

- [x] **Step 3: Write the implementation**

```go
package sqlite

import (
	"context"
	"database/sql"
)

// Target names a concrete database a shard opens: a libSQL DSN plus, for remote
// targets, an auth token. The token is held separately so error wrapping can
// redact it (the existing redactToken discipline).
type Target struct {
	dsn       string
	authToken string
}

// PartitionCatalog maps logical partitions to concrete database targets. It is
// the storage-location seam: local files, a shared sqld endpoint, per-partition
// sqld namespaces, or Turso databases all sit behind it. Mirrors the Rust
// PartitionCatalog trait.
type PartitionCatalog interface {
	// EnsureTarget provisions the partition's target if absent and returns it.
	// Idempotent.
	EnsureTarget(ctx context.Context, p Partition) (Target, error)
	// ExistingTarget returns the partition's target only if it already exists,
	// never creating storage. The bool is false when the partition is unknown.
	ExistingTarget(ctx context.Context, p Partition) (Target, bool, error)
	// Partitions enumerates every known partition. Single-target catalogs return
	// an empty slice (no enumeration).
	Partitions(ctx context.Context) ([]Partition, error)
	// PrepareShard runs once when a shard's database is first opened, recording
	// the partition's logical name. The default for single-database layouts is a
	// no-op.
	PrepareShard(ctx context.Context, p Partition, db *sql.DB) error
}

// Backend pairs a catalog with the strategy it serves; constructors build one.
type Backend struct {
	strategy PartitionStrategy
	catalog  PartitionCatalog
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run TestTarget -v && mise exec -- go build ./stores/sqlite/`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/catalog.go stores/sqlite/catalog_test.go -m "Added the Target type, Backend pairing, and PartitionCatalog interface"
```

---

## Task 6: Local catalog — single-file and b32 multi-file layouts

**Files:**
- Create: `stores/sqlite/catalog-local.go`
- Test: `stores/sqlite/catalog-local_test.go`

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalCatalogSingleFileForGlobal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.db")
	cat := newLocalCatalog(path, Global())

	tgt, err := cat.EnsureTarget(ctx, DefaultPartition())
	require.NoError(t, err)
	assert.Equal(t, "file:"+path, tgt.dsn)
}

func TestLocalCatalogB32FilePerNamedPartition(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cat := newLocalCatalog(dir, ByType())

	tgt, err := cat.EnsureTarget(ctx, MakePartition("order"))
	require.NoError(t, err)
	// base32(NoPadding) of "order" is "N5XWEYLS"; prefix "b32-".
	assert.Equal(t, "file:"+filepath.Join(dir, "b32-N5XWEYLS.db"), tgt.dsn)
}

func TestLocalCatalogExistingTargetReportsAbsence(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cat := newLocalCatalog(dir, ByType())

	_, ok, err := cat.ExistingTarget(ctx, MakePartition("order"))
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestLocalCatalogDiscoversWrittenPartitions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cat := newLocalCatalog(dir, ByType())

	for _, name := range []string{"order", "user"} {
		tgt, err := cat.EnsureTarget(ctx, MakePartition(name))
		require.NoError(t, err)
		db := openTarget(t, tgt)
		require.NoError(t, migrate(ctx, db))
		_ = db.Close()
	}

	parts, err := cat.Partitions(ctx)
	require.NoError(t, err)
	names := []string{parts[0].Name(), parts[1].Name()}
	sort.Strings(names)
	assert.Equal(t, []string{"order", "user"}, names)
}
```

Add this helper to the test file:

```go
func openTarget(t *testing.T, tgt Target) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, tgt.dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	return db
}
```

with imports `"database/sql"`.

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestLocalCatalog -v`
Expected: FAIL — `undefined: newLocalCatalog`.

- [x] **Step 3: Write the implementation**

```go
package sqlite

import (
	"context"
	"database/sql"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// localCatalog maps partitions to local SQLite files. The Global strategy uses
// root as a single .db file; named strategies use root as a directory holding
// one "b32-<BASE32_NOPAD(name)>.db" per partition. The base32 encoding (rather
// than the raw name) keeps the on-disk layout byte-compatible with wee-events.rs.
type localCatalog struct {
	root     string
	strategy PartitionStrategy
	single   bool
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newLocalCatalog(root string, strategy LocalStrategy) *localCatalog {
	_, single := strategy.(*global)
	return &localCatalog{root: root, strategy: strategy, single: single}
}

func (c *localCatalog) pathFor(p Partition) string {
	if c.single {
		return c.root
	}
	return filepath.Join(c.root, "b32-"+b32.EncodeToString([]byte(c.strategy.PartitionName(p)))+".db")
}

func (c *localCatalog) EnsureTarget(_ context.Context, p Partition) (Target, error) {
	path := c.pathFor(p)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Target{}, fmt.Errorf("sqlite: failed to create partition directory: %w", err)
	}
	return Target{dsn: "file:" + path}, nil
}

func (c *localCatalog) ExistingTarget(_ context.Context, p Partition) (Target, bool, error) {
	path := c.pathFor(p)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Target{}, false, nil
		}
		return Target{}, false, fmt.Errorf("sqlite: failed to stat partition file: %w", err)
	}
	return Target{dsn: "file:" + path}, true, nil
}

func (c *localCatalog) Partitions(_ context.Context) ([]Partition, error) {
	if c.single {
		return []Partition{DefaultPartition()}, nil
	}

	entries, err := os.ReadDir(c.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite: failed to list partition directory: %w", err)
	}

	var partitions []Partition
	for _, entry := range entries {
		name := entry.Name()
		encoded, ok := strings.CutPrefix(name, "b32-")
		if !ok || !strings.HasSuffix(encoded, ".db") {
			continue
		}
		decoded, err := b32.DecodeString(strings.TrimSuffix(encoded, ".db"))
		if err != nil {
			continue
		}
		partition, err := c.strategy.PartitionFromName(string(decoded))
		if err != nil {
			continue
		}
		partitions = append(partitions, partition)
	}
	return partitions, nil
}

func (c *localCatalog) PrepareShard(ctx context.Context, p Partition, db *sql.DB) error {
	if c.single {
		return nil
	}
	return ensurePartitionName(ctx, db, c.strategy.PartitionName(p))
}
```

Verify `base32(NoPadding)` of `"order"` is `N5XWEYLS`; if the test's expected filename differs, trust the encoder output and update the test literal (it is deterministic).

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run TestLocalCatalog -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/catalog-local.go stores/sqlite/catalog-local_test.go -m "Added the local catalog with single-file and base32 multi-file layouts and filesystem discovery"
```

---

## Task 7: Single-target catalog (in-memory, sqld-default)

**Files:**
- Create: `stores/sqlite/catalog-single.go`
- Test: `stores/sqlite/catalog-single_test.go`

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSingleTargetReturnsOneTargetForEveryPartition(t *testing.T) {
	ctx := context.Background()
	cat := newSingleTargetCatalog(Target{dsn: ":memory:"})

	a, err := cat.EnsureTarget(ctx, DefaultPartition())
	require.NoError(t, err)
	b, err := cat.EnsureTarget(ctx, MakePartition("anything"))
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestSingleTargetEnumeratesEmpty(t *testing.T) {
	ctx := context.Background()
	cat := newSingleTargetCatalog(Target{dsn: ":memory:"})
	parts, err := cat.Partitions(ctx)
	require.NoError(t, err)
	assert.Empty(t, parts)
}

func TestSingleTargetExistingAlwaysPresent(t *testing.T) {
	ctx := context.Background()
	cat := newSingleTargetCatalog(Target{dsn: ":memory:"})
	_, ok, err := cat.ExistingTarget(ctx, DefaultPartition())
	require.NoError(t, err)
	assert.True(t, ok)
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestSingleTarget -v`
Expected: FAIL — `undefined: newSingleTargetCatalog`.

- [x] **Step 3: Write the implementation**

```go
package sqlite

import (
	"context"
	"database/sql"
)

// singleTargetCatalog maps every partition to one provisioned target. It backs
// the in-memory and sqld-default backends, where all aggregates share one
// database. Enumeration over a single target returns no partitions; callers
// fall back to the store's known set.
type singleTargetCatalog struct {
	target Target
}

func newSingleTargetCatalog(target Target) *singleTargetCatalog {
	return &singleTargetCatalog{target: target}
}

func (c *singleTargetCatalog) EnsureTarget(context.Context, Partition) (Target, error) {
	return c.target, nil
}
func (c *singleTargetCatalog) ExistingTarget(context.Context, Partition) (Target, bool, error) {
	return c.target, true, nil
}
func (c *singleTargetCatalog) Partitions(context.Context) ([]Partition, error) {
	return nil, nil
}
func (c *singleTargetCatalog) PrepareShard(context.Context, Partition, *sql.DB) error {
	return nil
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run TestSingleTarget -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/catalog-single.go stores/sqlite/catalog-single_test.go -m "Added the single-target catalog for the in-memory and sqld-default backends"
```

---

## Task 8: Provisioner interface, named catalog, and sqld provisioner

**Files:**
- Create: `stores/sqlite/catalog-named.go`, `stores/sqlite/provisioner-sqld.go`
- Test: `stores/sqlite/catalog-named_test.go`

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProvisioner is an in-memory NamedTargetProvisioner for catalog tests.
type fakeProvisioner struct {
	targets map[string]Target
}

func newFakeProvisioner() *fakeProvisioner {
	return &fakeProvisioner{targets: map[string]Target{}}
}

func (f *fakeProvisioner) EnsureTarget(_ context.Context, name PartitionName) (Target, error) {
	key := name.String()
	if tgt, ok := f.targets[key]; ok {
		return tgt, nil
	}
	tgt := Target{dsn: "fake://" + key}
	f.targets[key] = tgt
	return tgt, nil
}

func (f *fakeProvisioner) ExistingTarget(_ context.Context, name PartitionName) (Target, bool, error) {
	tgt, ok := f.targets[name.String()]
	return tgt, ok, nil
}

func (f *fakeProvisioner) NamedTargets(context.Context) ([]NamedTarget, error) {
	out := make([]NamedTarget, 0, len(f.targets))
	for name, tgt := range f.targets {
		out = append(out, NamedTarget{Name: name, Target: tgt})
	}
	return out, nil
}

func TestNamedCatalogEnsureUsesPartitionName(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvisioner()
	cat := newNamedTargetCatalog(ByType(), prov)

	tgt, err := cat.EnsureTarget(ctx, MakePartition("order"))
	require.NoError(t, err)
	assert.Equal(t, "fake://order", tgt.dsn)
}

func TestNamedCatalogPartitionsRoundTripThroughStrategy(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvisioner()
	cat := newNamedTargetCatalog(ByType(), prov)

	_, err := cat.EnsureTarget(ctx, MakePartition("order"))
	require.NoError(t, err)
	_, err = cat.EnsureTarget(ctx, MakePartition("user"))
	require.NoError(t, err)

	parts, err := cat.Partitions(ctx)
	require.NoError(t, err)
	names := []string{parts[0].Name(), parts[1].Name()}
	sort.Strings(names)
	assert.Equal(t, []string{"order", "user"}, names)
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestNamedCatalog -v`
Expected: FAIL — `undefined: PartitionName`.

- [x] **Step 3a: Write the named catalog and provisioner interface (`catalog-named.go`)**

```go
package sqlite

import (
	"context"
	"database/sql"
	"sort"
)

// PartitionName is the wire name of a partition passed to a provisioner. The
// default partition maps to a backend-specific default; named partitions carry
// the strategy name.
type PartitionName struct {
	name      string
	isDefault bool
}

func (n PartitionName) String() string  { return n.name }
func (n PartitionName) IsDefault() bool  { return n.isDefault }

func partitionName(strategy PartitionStrategy, p Partition) PartitionName {
	if p.IsDefault() {
		return PartitionName{isDefault: true}
	}
	return PartitionName{name: strategy.PartitionName(p)}
}

// NamedTarget pairs a provisioned database's wire name with its target.
type NamedTarget struct {
	Name   string
	Target Target
}

// Provisioner creates and lists per-partition databases for the named-target
// backends (sqld namespaces, Turso platform databases).
type Provisioner interface {
	EnsureTarget(ctx context.Context, name PartitionName) (Target, error)
	ExistingTarget(ctx context.Context, name PartitionName) (Target, bool, error)
	NamedTargets(ctx context.Context) ([]NamedTarget, error)
}

// namedTargetCatalog maps each partition to its own provisioned database.
type namedTargetCatalog struct {
	strategy    PartitionStrategy
	provisioner Provisioner
}

func newNamedTargetCatalog(strategy NamingStrategy, provisioner Provisioner) *namedTargetCatalog {
	return &namedTargetCatalog{strategy: strategy, provisioner: provisioner}
}

func (c *namedTargetCatalog) EnsureTarget(ctx context.Context, p Partition) (Target, error) {
	return c.provisioner.EnsureTarget(ctx, partitionName(c.strategy, p))
}

func (c *namedTargetCatalog) ExistingTarget(ctx context.Context, p Partition) (Target, bool, error) {
	return c.provisioner.ExistingTarget(ctx, partitionName(c.strategy, p))
}

// Partitions discovers partitions by listing provisioned databases and reading
// each one's recorded logical name, falling back to strategy discovery for
// databases that predate the metadata table. Mirrors the Rust three-step.
func (c *namedTargetCatalog) Partitions(ctx context.Context) ([]Partition, error) {
	named, err := c.provisioner.NamedTargets(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]Partition{}
	for _, nt := range named {
		logical, err := c.logicalName(ctx, nt)
		if err != nil {
			return nil, err
		}
		if logical == "" {
			continue
		}
		partition, err := c.strategy.PartitionFromName(logical)
		if err != nil {
			continue
		}
		seen[partition.Name()] = partition
	}

	partitions := make([]Partition, 0, len(seen))
	for _, p := range seen {
		partitions = append(partitions, p)
	}
	sort.Slice(partitions, func(i, j int) bool { return partitions[i].Name() < partitions[j].Name() })
	return partitions, nil
}

// logicalName reads a database's recorded partition name, opening it directly.
func (c *namedTargetCatalog) logicalName(ctx context.Context, nt NamedTarget) (string, error) {
	db, err := sql.Open(driverName, nt.Target.dsn)
	if err != nil {
		return "", redactToken(err, nt.Target.authToken)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	name, err := readPartitionName(ctx, db)
	if err != nil {
		return "", redactToken(err, nt.Target.authToken)
	}
	return name, nil
}

func (c *namedTargetCatalog) PrepareShard(ctx context.Context, p Partition, db *sql.DB) error {
	return ensurePartitionName(ctx, db, c.strategy.PartitionName(p))
}
```

- [x] **Step 3b: Write the sqld provisioner (`provisioner-sqld.go`)**

```go
package sqlite

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// sqldProvisioner provisions per-partition namespaces on a single sqld server.
// A namespace is created via the admin API; a 200 or an already-exists 409 is
// success. Databases are reached at the data URL with the namespace as a host
// subdomain, matching sqld's namespace addressing.
type sqldProvisioner struct {
	adminURL  string
	dataURL   string
	authToken string
	http      *http.Client
}

func newSqldProvisioner(adminURL, dataURL, authToken string) *sqldProvisioner {
	return &sqldProvisioner{
		adminURL:  strings.TrimRight(adminURL, "/"),
		dataURL:   strings.TrimRight(dataURL, "/"),
		authToken: authToken,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *sqldProvisioner) namespace(name PartitionName) string {
	if name.IsDefault() {
		return "default"
	}
	return name.String()
}

func (p *sqldProvisioner) targetFor(namespace string) Target {
	return Target{dsn: p.namespaceURL(namespace), authToken: p.authToken}
}

func (p *sqldProvisioner) namespaceURL(namespace string) string {
	// libsql://<namespace>.<host> — split scheme and host of dataURL.
	scheme, host, found := strings.Cut(p.dataURL, "://")
	if !found {
		return p.dataURL
	}
	return fmt.Sprintf("%s://%s.%s", scheme, namespace, host)
}

func (p *sqldProvisioner) EnsureTarget(ctx context.Context, name PartitionName) (Target, error) {
	namespace := p.namespace(name)
	url := fmt.Sprintf("%s/v1/namespaces/%s/create", p.adminURL, namespace)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		return Target{}, fmt.Errorf("sqlite: failed to build namespace request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.authToken)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return Target{}, fmt.Errorf("sqlite: namespace create failed: %w", err)
	}
	// The body is irrelevant; close it so the connection can be reused.
	_ = resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		// 409 means the namespace already exists, which is success for an
		// idempotent ensure.
		return p.targetFor(namespace), nil
	default:
		return Target{}, fmt.Errorf("sqlite: namespace create returned status %d", resp.StatusCode)
	}
}

func (p *sqldProvisioner) ExistingTarget(_ context.Context, name PartitionName) (Target, bool, error) {
	// sqld has no cheap existence probe distinct from create; the store opens
	// the target lazily and treats a missing namespace as a load miss. Report
	// the addressable target and let the caller's open decide.
	return p.targetFor(p.namespace(name)), true, nil
}

func (p *sqldProvisioner) NamedTargets(_ context.Context) ([]NamedTarget, error) {
	// The sqld admin API used here does not enumerate namespaces; discovery for
	// sqld relies on the store's known set. Returning empty keeps Partitions a
	// union with known.
	return nil, nil
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run TestNamedCatalog -v && mise exec -- go build ./stores/sqlite/`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/catalog-named.go stores/sqlite/provisioner-sqld.go stores/sqlite/catalog-named_test.go -m "Added the named-target catalog, provisioner interface, and sqld namespace provisioner"
```

---

## Task 9: Shard actor

**Files:**
- Create: `stores/sqlite/shard.go`
- Modify: `stores/sqlite/store.go` (move `Load`/`publishOnce` helpers the shard reuses — see note)
- Test: `stores/sqlite/shard_test.go`

> Note: the shard reuses the existing publish/load machinery. Move these from `store.go` into `shard.go` (or keep them in `store.go` — either is fine as long as they compile): `publishOnce`, `currentSequence`, `checkExpectedRevision`, `encodeEvents`, `eventRow`, `nullable`, `loadEvents` (extract the body of the old `Load` into a free function `loadEvents(ctx, db, id)`), plus the `applyBusyTimeout` call. The shard owns a `*sql.DB` (not `*sql.Conn`); adapt `publishOnce`/`currentSequence` to take `*sql.DB` and call `db.Conn`/`ExecContext` as today.

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func newTestShard(t *testing.T) *shard {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open(driverName, ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrate(ctx, db))
	sh := newShard(db, we.MakeJSONEncoder(), defaultBusyTimeout)
	t.Cleanup(sh.stop)
	return sh
}

func TestShardPublishThenLoad(t *testing.T) {
	ctx := context.Background()
	sh := newTestShard(t)
	id := we.AggregateId{Type: "order", Key: "1"}

	err := sh.publish(ctx, id, we.Options(), testEvent{Value: "a"})
	require.NoError(t, err)

	agg, err := sh.load(ctx, id)
	require.NoError(t, err)
	require.Len(t, agg.Events, 1)
	assert.Equal(t, we.EventType("test-event"), agg.Events[0].EventType)
}

func TestShardSerializesConcurrentPublishes(t *testing.T) {
	ctx := context.Background()
	sh := newTestShard(t)
	id := we.AggregateId{Type: "order", Key: "1"}

	const n = 32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- sh.publish(ctx, id, we.Options(), testEvent{Value: "x"}) }()
	}
	for i := 0; i < n; i++ {
		require.NoError(t, <-errs)
	}

	agg, err := sh.load(ctx, id)
	require.NoError(t, err)
	assert.Len(t, agg.Events, n)
}

func TestShardRespectsContextCancellation(t *testing.T) {
	sh := newTestShard(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := sh.load(ctx, we.AggregateId{Type: "order", Key: "1"})
	require.ErrorIs(t, err, context.Canceled)
}
```

`testEvent` already exists in `store_test.go`; its `EventTypeOf` resolves to `test-event` only if the type name maps that way — adjust the asserted `EventType` literal to whatever `we.EventTypeOf(testEvent{})` actually returns (run once and read the value).

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestShard -v`
Expected: FAIL — `undefined: newShard`.

- [x] **Step 3: Write the implementation**

```go
package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/weegigs/wee-events-go/we"
)

// shard owns one partition's database exclusively. A single goroutine consumes
// the request channel and runs every load/publish/scan against the pinned
// connection, so concurrent callers are serialised without touching the
// database from more than one goroutine — the invariant that resolves the
// go-libsql SQLITE_MISUSE concurrency defect. SetMaxOpenConns(1) holds the
// invariant even if a future caller bypasses the channel.
type shard struct {
	db          *sql.DB
	encoder     we.Encoder
	busyTimeout time.Duration
	requests    chan shardRequest
	done        chan struct{}
}

type shardRequest struct {
	ctx   context.Context
	run   func(ctx context.Context, db *sql.DB) (any, error)
	reply chan shardResult
}

type shardResult struct {
	value any
	err   error
}

func newShard(db *sql.DB, encoder we.Encoder, busyTimeout time.Duration) *shard {
	sh := &shard{
		db:          db,
		encoder:     encoder,
		busyTimeout: busyTimeout,
		requests:    make(chan shardRequest),
		done:        make(chan struct{}),
	}
	go sh.serve()
	return sh
}

func (s *shard) serve() {
	for {
		select {
		case <-s.done:
			return
		case req := <-s.requests:
			value, err := req.run(req.ctx, s.db)
			req.reply <- shardResult{value: value, err: err}
		}
	}
}

// dispatch hands an operation to the owner goroutine and waits for its result
// or the caller's cancellation. The reply channel is buffered so the owner is
// never blocked writing a result the caller has abandoned.
func (s *shard) dispatch(ctx context.Context, run func(ctx context.Context, db *sql.DB) (any, error)) (any, error) {
	reply := make(chan shardResult, 1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, errStoreClosed
	case s.requests <- shardRequest{ctx: ctx, run: run, reply: reply}:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-reply:
		return res.value, res.err
	}
}

func (s *shard) load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	value, err := s.dispatch(ctx, func(ctx context.Context, db *sql.DB) (any, error) {
		return loadEvents(ctx, db, id)
	})
	if err != nil {
		return we.Aggregate{}, err
	}
	return value.(we.Aggregate), nil
}

func (s *shard) publish(ctx context.Context, id we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	if len(events) == 0 {
		// Empty publish is a no-op state, not an error (matches the suite).
		return nil
	}
	encoder, err := options.EncoderFor(s.encoder)
	if err != nil {
		return fmt.Errorf("sqlite: %w", err)
	}
	rows, err := encodeEvents(encoder, options, events)
	if err != nil {
		return err
	}

	_, err = s.dispatch(ctx, func(ctx context.Context, db *sql.DB) (any, error) {
		return nil, publishRows(ctx, db, s.busyTimeout, id, options, rows)
	})
	return err
}

// scan runs an arbitrary read on the owner goroutine; enumeration uses it.
func (s *shard) scan(ctx context.Context, run func(ctx context.Context, db *sql.DB) ([]we.AggregateId, error)) ([]we.AggregateId, error) {
	value, err := s.dispatch(ctx, func(ctx context.Context, db *sql.DB) (any, error) {
		return run(ctx, db)
	})
	if err != nil {
		return nil, err
	}
	return value.([]we.AggregateId), nil
}

func (s *shard) stop() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	_ = s.db.Close()
}
```

Add `import "fmt"`. Define `errStoreClosed` in `store.go` (Task 10) as `errors.New("sqlite: store is closed")`; for this task's tests it is referenced only on the closed path, so add a temporary `var errStoreClosed = errors.New("sqlite: store is closed")` in `shard.go` and remove it in Task 10 if it moves. `publishRows` is the busy-retry loop extracted from the old `Publish` (wrapping `publishOnce`): create it in `shard.go`:

```go
// publishRows runs the publish transaction with bounded busy retries. Revision
// conflicts are terminal and never retried (SQLITE-S2.R5).
func publishRows(ctx context.Context, db *sql.DB, busyTimeout time.Duration, id we.AggregateId, options we.PublishOptions, rows []eventRow) error {
	var lastErr error
	for attempt := 0; attempt <= busyRetries; attempt++ {
		err := publishOnce(ctx, db, busyTimeout, id, options, rows)
		if err == nil {
			return nil
		}
		if errors.Is(err, we.RevisionConflict) {
			return err
		}
		if !isBusy(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}
```

Adapt `publishOnce` and `currentSequence` signatures to `(ctx, db *sql.DB, busyTimeout, …)` doing `db.Conn(ctx)` internally as the old code did. Add `import "errors"`.

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run TestShard -race -v`
Expected: PASS, no race.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/shard.go stores/sqlite/store.go stores/sqlite/shard_test.go -m "Added the single-owner shard actor that serialises load and publish over one connection"
```

---

## Task 10: Rewrite the Store — routing, constructors, Close

**Files:**
- Modify (rewrite): `stores/sqlite/store.go`
- Modify: `stores/sqlite/store_test.go` (update `newMemoryStore`/`newFileStore` to new constructors)
- Test: covered by the existing validation suite tests, re-pointed.

This task replaces the old single-database `Store`, `config`, `NewStore`, and the `InMemory`/`LocalFile`/`Remote`/`BusyTimeout` Options with the partitioned store and backend constructors. It is the task where the old API is deleted, because its last callers (`store_test.go`, `store_bench_test.go`) are migrated in the same change.

- [x] **Step 1: Update the test helpers to the target API (failing)**

In `store_test.go`, replace the two helpers:

```go
func newMemoryStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func newFileStore(t *testing.T, path string) *Store {
	t.Helper()
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(path, Global()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}
```

- [x] **Step 2: Run to verify it fails**

Run: `mise exec -- go build ./stores/sqlite/`
Expected: FAIL — `too many arguments to InMemory` / `undefined: Local`.

- [x] **Step 3: Write the new `store.go`**

```go
package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	_ "github.com/tursodatabase/go-libsql"

	"github.com/weegigs/wee-events-go/we"
)

const driverName = "libsql"

var errStoreClosed = errors.New("sqlite: store is closed")

// Store is a partitioned SQLite/libSQL event store. A PartitionStrategy routes
// each aggregate to a Partition; a PartitionCatalog maps the partition to a
// database target; each partition is owned by one shard goroutine. The store
// satisfies we.EventStore (SQLITE-S1.R1).
type Store struct {
	strategy PartitionStrategy
	catalog  PartitionCatalog
	encoder  we.Encoder

	mu     sync.Mutex
	shards map[Partition]*shard
	known  map[Partition]struct{}
	closed bool
}

var _ we.EventStore = (*Store)(nil)

// NewStore opens a partitioned store over the given backend. The encoder is the
// store's explicit write encoding (ENCODING-S2.R1); nil is a construction error.
// The store is never returned half-built.
func NewStore(_ context.Context, encoder we.Encoder, backend Backend) (*Store, error) {
	if encoder == nil {
		return nil, errors.New("sqlite: encoder is required")
	}
	return &Store{
		strategy: backend.strategy,
		catalog:  backend.catalog,
		encoder:  encoder,
		shards:   map[Partition]*shard{},
		known:    map[Partition]struct{}{},
	}, nil
}

// Close stops every shard and releases every database. Pairs with NewStore.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for _, sh := range s.shards {
		sh.stop()
	}
	s.shards = map[Partition]*shard{}
	return nil
}

func (s *Store) Load(ctx context.Context, id we.AggregateId) (we.Aggregate, error) {
	partition := s.strategy.PartitionFor(id)
	sh, ok, err := s.openExisting(ctx, partition)
	if err != nil {
		return we.Aggregate{}, err
	}
	if !ok {
		// A partition that was never provisioned holds no events; this is a
		// state, not an error.
		return we.Aggregate{Id: id, Revision: we.InitialRevision}, nil
	}
	return sh.load(ctx, id)
}

func (s *Store) Publish(ctx context.Context, id we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	partition := s.strategy.PartitionFor(id)
	sh, err := s.ensureShard(ctx, partition)
	if err != nil {
		return err
	}
	return sh.publish(ctx, id, options, events...)
}

// ensureShard returns the partition's shard, provisioning and opening it on
// first use. The shard map grows monotonically; unbounded strategies
// (ByAggregate) trade memory for isolation, as documented.
func (s *Store) ensureShard(ctx context.Context, p Partition) (*shard, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errStoreClosed
	}
	if sh, ok := s.shards[p]; ok {
		s.mu.Unlock()
		return sh, nil
	}
	s.mu.Unlock()

	target, err := s.catalog.EnsureTarget(ctx, p)
	if err != nil {
		return nil, err
	}
	sh, err := s.openShard(ctx, p, target)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		sh.stop()
		return nil, errStoreClosed
	}
	if existing, ok := s.shards[p]; ok {
		// Another goroutine opened it first; discard the duplicate.
		sh.stop()
		return existing, nil
	}
	s.shards[p] = sh
	s.known[p] = struct{}{}
	return sh, nil
}

// openExisting returns the partition's shard if its target already exists,
// without provisioning. Used by Load.
func (s *Store) openExisting(ctx context.Context, p Partition) (*shard, bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, false, errStoreClosed
	}
	if sh, ok := s.shards[p]; ok {
		s.mu.Unlock()
		return sh, true, nil
	}
	s.mu.Unlock()

	target, ok, err := s.catalog.ExistingTarget(ctx, p)
	if err != nil || !ok {
		return nil, false, err
	}
	sh, err := s.openShard(ctx, p, target)
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		sh.stop()
		return nil, false, errStoreClosed
	}
	if existing, ok := s.shards[p]; ok {
		sh.stop()
		return existing, true, nil
	}
	s.shards[p] = sh
	s.known[p] = struct{}{}
	return sh, true, nil
}

// openShard opens and migrates a target, records its partition name, and starts
// its owner goroutine.
func (s *Store) openShard(ctx context.Context, p Partition, target Target) (*shard, error) {
	db, err := sql.Open(driverName, target.dsn)
	if err != nil {
		return nil, redactToken(fmt.Errorf("sqlite: failed to open database: %w", err), target.authToken)
	}
	db.SetMaxOpenConns(1)

	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, redactToken(err, target.authToken)
	}
	if err := s.catalog.PrepareShard(ctx, p, db); err != nil {
		_ = db.Close()
		return nil, redactToken(err, target.authToken)
	}
	return newShard(db, s.encoder, defaultBusyTimeout), nil
}
```

Add `import "database/sql"`. Keep `defaultBusyTimeout`, `busyRetries`, `redactToken`, `isBusy`, `isUniqueViolation`, `newEventID`, `revisionForSequence`, `sequenceForRevision`, `timestampFromEventID`, `applyBusyTimeout`, and the relocated `loadEvents`/`publishOnce`/`currentSequence`/`checkExpectedRevision`/`encodeEvents`/`eventRow`/`nullable` (from Task 9). Delete the old `config`, Options (`InMemory`/`LocalFile`/`Remote`/`BusyTimeout`), the old `NewStore`, `Store.prepare`, and the struct fields they used. `applyPragmas` collapses into `applyBusyTimeout` if WAL/other pragmas were set there — preserve any non-busy pragmas by calling them inside `openShard` after `migrate`.

- [x] **Step 4: Write backend constructors at the end of `store.go`**

```go
// Local builds a backend backed by local SQLite files under root. Global uses
// root as a single file; named strategies use it as a directory.
func Local(root string, strategy LocalStrategy) Backend {
	return Backend{strategy: strategy, catalog: newLocalCatalog(root, strategy)}
}

// InMemory builds a single private in-memory database. Only single-target
// strategies are legal.
func InMemory(strategy SingleTargetStrategy) Backend {
	return Backend{strategy: strategy, catalog: newSingleTargetCatalog(Target{dsn: ":memory:"})}
}

// SqldDefault builds a backend over one shared sqld endpoint. Only
// single-target strategies are legal.
func SqldDefault(url, authToken string, strategy SingleTargetStrategy) Backend {
	return Backend{strategy: strategy, catalog: newSingleTargetCatalog(Target{dsn: url, authToken: authToken})}
}

// SqldNamespaced builds a backend that provisions one sqld namespace per
// partition. adminURL is the namespace admin endpoint; dataURL is the data
// endpoint partitions are addressed under.
func SqldNamespaced(adminURL, dataURL, authToken string, strategy NamingStrategy) Backend {
	provisioner := newSqldProvisioner(adminURL, dataURL, authToken)
	return Backend{strategy: strategy, catalog: newNamedTargetCatalog(strategy, provisioner)}
}
```

(`Turso` constructor is added in Task 12.)

- [x] **Step 5: Run the validation suite to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run "ValidationSuite|TestLoadInitial|Shard|Catalog|Strategy|Hashed|Migrate|Target" -v`
Expected: PASS — in-memory and local-file conformance green on the new store.

- [x] **Step 6: Lint and commit**

Run: `mise exec -- gofmt -s -w stores/sqlite/ && mise exec -- golangci-lint run ./stores/sqlite/...`
Expected: 0 issues.

```bash
jj split stores/sqlite/store.go stores/sqlite/store_test.go -m "Rewrote the SQLite store as a partitioned, single-owner-per-shard router and replaced the single-database constructors"
```

---

## Task 11: Aggregate enumeration

**Files:**
- Create: `stores/sqlite/enumerate.go`
- Test: `stores/sqlite/enumerate_test.go`

- [x] **Step 1: Write the failing test**

```go
package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func TestEnumerateAcrossTypePartitions(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), ByType()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ids := []we.AggregateId{
		{Type: "order", Key: "1"},
		{Type: "order", Key: "2"},
		{Type: "user", Key: "kevin"},
	}
	for _, id := range ids {
		require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}))
	}

	got, err := store.EnumerateAggregates(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, got)
}

func TestEnumerateByTypeNarrows(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), ByType()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	for _, id := range []we.AggregateId{{Type: "order", Key: "1"}, {Type: "user", Key: "kevin"}} {
		require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}))
	}

	got, err := store.EnumerateAggregatesByType(ctx, "order")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "order", got[0].Type)
}

func TestEnumerateByAggregateUsesDirect(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), ByAggregate()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	id := we.AggregateId{Type: "order", Key: "1"}
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}))

	got, err := store.EnumerateAggregates(ctx)
	require.NoError(t, err)
	assert.Equal(t, []we.AggregateId{id}, got)
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestEnumerate -v`
Expected: FAIL — `undefined: (*Store).EnumerateAggregates`.

- [x] **Step 3: Write the implementation**

```go
package sqlite

import (
	"context"
	"database/sql"
	"sort"

	"github.com/weegigs/wee-events-go/we"
)

// EnumerateAggregates returns every aggregate id across all known partitions.
// It unions the catalog's discovered partitions with partitions the store has
// touched this session, then harvests ids per partition by read plan.
func (s *Store) EnumerateAggregates(ctx context.Context) ([]we.AggregateId, error) {
	return s.enumerate(ctx, func(p Partition) ReadPlan { return s.strategy.ReadPlan(p) })
}

// EnumerateAggregatesByType returns every aggregate of the given type. Type
// partitions that cannot hold the type are skipped; other partitions scan and
// filter.
func (s *Store) EnumerateAggregatesByType(ctx context.Context, aggregateType string) ([]we.AggregateId, error) {
	return s.enumerate(ctx, func(p Partition) ReadPlan {
		plan := s.strategy.ReadPlan(p)
		switch plan.kind {
		case readScanType:
			if plan.aggregateType != aggregateType {
				return Skip()
			}
			return plan
		case readDirect:
			if plan.id.Type != aggregateType {
				return Skip()
			}
			return plan
		default:
			return plan
		}
	})
}

func (s *Store) enumerate(ctx context.Context, planFor func(Partition) ReadPlan) ([]we.AggregateId, error) {
	partitions, err := s.allKnownPartitions(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]we.AggregateId{}
	for _, partition := range partitions {
		plan := planFor(partition)
		if plan.kind == readSkip {
			continue
		}
		if plan.kind == readDirect {
			seen[plan.id.Encode().String()] = plan.id
			continue
		}

		sh, ok, err := s.openExisting(ctx, partition)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ids, err := sh.scan(ctx, func(ctx context.Context, db *sql.DB) ([]we.AggregateId, error) {
			return scanAggregates(ctx, db, plan)
		})
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id.Encode().String()] = id
		}
	}

	out := make([]we.AggregateId, 0, len(seen))
	for _, id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Encode().String() < out[j].Encode().String() })
	return out, nil
}

// allKnownPartitions unions catalog discovery with the store's touched set.
func (s *Store) allKnownPartitions(ctx context.Context) ([]Partition, error) {
	discovered, err := s.catalog.Partitions(ctx)
	if err != nil {
		return nil, err
	}

	set := map[Partition]struct{}{}
	for _, p := range discovered {
		set[p] = struct{}{}
	}
	s.mu.Lock()
	for p := range s.known {
		set[p] = struct{}{}
	}
	s.mu.Unlock()

	partitions := make([]Partition, 0, len(set))
	for p := range set {
		partitions = append(partitions, p)
	}
	return partitions, nil
}

// scanAggregates returns the distinct aggregate ids in a partition, honouring a
// ScanType narrowing.
func scanAggregates(ctx context.Context, db *sql.DB, plan ReadPlan) ([]we.AggregateId, error) {
	query := `SELECT DISTINCT aggregate_type, aggregate_key FROM events`
	args := []any{}
	if plan.kind == readScanType {
		query += ` WHERE aggregate_type = ?`
		args = append(args, plan.aggregateType)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to enumerate aggregates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []we.AggregateId
	for rows.Next() {
		var t, k string
		if err := rows.Scan(&t, &k); err != nil {
			return nil, fmt.Errorf("sqlite: failed to scan aggregate id: %w", err)
		}
		ids = append(ids, we.AggregateId{Type: t, Key: k})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: failed to read aggregate ids: %w", err)
	}
	return ids, nil
}
```

Add `import "fmt"`. Remove the stray `sort.StringSlice` line from the test if the linter flags it.

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run TestEnumerate -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/enumerate.go stores/sqlite/enumerate_test.go -m "Added aggregate enumeration with per-partition read-plan dispatch"
```

---

## Task 12: Turso platform client and provisioner

**Files:**
- Create: `stores/sqlite/turso-client.go`, `stores/sqlite/turso-provisioner.go`
- Modify: `stores/sqlite/store.go` (add `Turso` constructor + `TursoConfig`)
- Test: `stores/sqlite/turso-provisioner_test.go`

- [x] **Step 1: Write the failing test (against the fake client)**

```go
package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTursoProvisionerCreatesPrefixedDatabase(t *testing.T) {
	ctx := context.Background()
	client := newFakeTursoClient()
	prov := newTursoProvisioner(client, TursoConfig{Group: "g", Prefix: "we", GroupToken: "tok"})

	tgt, err := prov.EnsureTarget(ctx, PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "libsql://we-order-g.turso.io", tgt.dsn)
	assert.Equal(t, "tok", tgt.authToken)
	assert.Contains(t, client.created, "we-order")
}

func TestTursoProvisionerToleratesAlreadyExists(t *testing.T) {
	ctx := context.Background()
	client := newFakeTursoClient()
	client.existing["we-order"] = "we-order-g.turso.io"
	prov := newTursoProvisioner(client, TursoConfig{Group: "g", Prefix: "we", GroupToken: "tok"})

	tgt, err := prov.EnsureTarget(ctx, PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "libsql://we-order-g.turso.io", tgt.dsn)
}

func TestTursoProvisionerListsNamedTargets(t *testing.T) {
	ctx := context.Background()
	client := newFakeTursoClient()
	prov := newTursoProvisioner(client, TursoConfig{Group: "g", Prefix: "we", GroupToken: "tok"})

	_, err := prov.EnsureTarget(ctx, PartitionName{name: "order"})
	require.NoError(t, err)
	_, err = prov.EnsureTarget(ctx, PartitionName{name: "user"})
	require.NoError(t, err)

	named, err := prov.NamedTargets(ctx)
	require.NoError(t, err)
	assert.Len(t, named, 2)
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./stores/sqlite/ -run TestTursoProvisioner -v`
Expected: FAIL — `undefined: newFakeTursoClient`.

- [x] **Step 3a: Write the client interface, HTTP impl, and fake (`turso-client.go`)**

```go
package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// tursoDatabase is the platform API's database record (subset used here).
type tursoDatabase struct {
	Name     string `json:"Name"`
	Hostname string `json:"Hostname"`
}

// tursoClient is the Turso Platform API surface the provisioner needs. The
// real implementation calls the HTTP API; tests use an in-memory fake.
type tursoClient interface {
	CreateDatabase(ctx context.Context, org, group, name string) (tursoDatabase, bool, error) // bool: already existed
	GetDatabase(ctx context.Context, org, name string) (tursoDatabase, bool, error)
	ListDatabases(ctx context.Context, org string) ([]tursoDatabase, error)
	DeleteDatabase(ctx context.Context, org, name string) error
}

type httpTursoClient struct {
	apiToken string
	baseURL  string
	http     *http.Client
}

func newHTTPTursoClient(apiToken string) *httpTursoClient {
	return &httpTursoClient{
		apiToken: apiToken,
		baseURL:  "https://api.turso.tech/v1",
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *httpTursoClient) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("sqlite: failed to encode turso request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, fmt.Errorf("sqlite: failed to build turso request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("sqlite: turso request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if out != nil && resp.StatusCode/100 == 2 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("sqlite: failed to decode turso response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func (c *httpTursoClient) CreateDatabase(ctx context.Context, org, group, name string) (tursoDatabase, bool, error) {
	var out struct {
		Database tursoDatabase `json:"database"`
	}
	status, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/organizations/%s/databases", org),
		map[string]string{"name": name, "group": group}, &out)
	if err != nil {
		return tursoDatabase{}, false, err
	}
	switch status {
	case http.StatusOK, http.StatusCreated:
		return out.Database, false, nil
	case http.StatusConflict:
		db, ok, err := c.GetDatabase(ctx, org, name)
		return db, ok, err
	default:
		return tursoDatabase{}, false, fmt.Errorf("sqlite: turso create returned status %d", status)
	}
}

func (c *httpTursoClient) GetDatabase(ctx context.Context, org, name string) (tursoDatabase, bool, error) {
	var out struct {
		Database tursoDatabase `json:"database"`
	}
	status, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/organizations/%s/databases/%s", org, name), nil, &out)
	if err != nil {
		return tursoDatabase{}, false, err
	}
	if status == http.StatusNotFound {
		return tursoDatabase{}, false, nil
	}
	if status/100 != 2 {
		return tursoDatabase{}, false, fmt.Errorf("sqlite: turso get returned status %d", status)
	}
	return out.Database, true, nil
}

func (c *httpTursoClient) ListDatabases(ctx context.Context, org string) ([]tursoDatabase, error) {
	var out struct {
		Databases []tursoDatabase `json:"databases"`
	}
	status, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/organizations/%s/databases", org), nil, &out)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("sqlite: turso list returned status %d", status)
	}
	return out.Databases, nil
}

func (c *httpTursoClient) DeleteDatabase(ctx context.Context, org, name string) error {
	status, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/organizations/%s/databases/%s", org, name), nil, nil)
	if err != nil {
		return err
	}
	if status/100 != 2 && status != http.StatusNotFound {
		return fmt.Errorf("sqlite: turso delete returned status %d", status)
	}
	return nil
}
```

Add the fake to the test file `turso-provisioner_test.go`:

```go
type fakeTursoClient struct {
	created  map[string]string // name -> hostname
	existing map[string]string
}

func newFakeTursoClient() *fakeTursoClient {
	return &fakeTursoClient{created: map[string]string{}, existing: map[string]string{}}
}

func (f *fakeTursoClient) hostname(org, name string) string { return name + "-g.turso.io" }

func (f *fakeTursoClient) CreateDatabase(_ context.Context, org, group, name string) (tursoDatabase, bool, error) {
	if host, ok := f.existing[name]; ok {
		return tursoDatabase{Name: name, Hostname: host}, true, nil
	}
	host := f.hostname(org, name)
	f.created[name] = host
	f.existing[name] = host
	return tursoDatabase{Name: name, Hostname: host}, false, nil
}

func (f *fakeTursoClient) GetDatabase(_ context.Context, org, name string) (tursoDatabase, bool, error) {
	host, ok := f.existing[name]
	return tursoDatabase{Name: name, Hostname: host}, ok, nil
}

func (f *fakeTursoClient) ListDatabases(_ context.Context, org string) ([]tursoDatabase, error) {
	out := make([]tursoDatabase, 0, len(f.existing))
	for name, host := range f.existing {
		out = append(out, tursoDatabase{Name: name, Hostname: host})
	}
	return out, nil
}

func (f *fakeTursoClient) DeleteDatabase(_ context.Context, org, name string) error {
	delete(f.existing, name)
	delete(f.created, name)
	return nil
}
```

(The fake's `hostname` hardcodes `-g` to match the test's expected DSN; the group name `g` is incidental.)

- [x] **Step 3b: Write the Turso provisioner (`turso-provisioner.go`)**

```go
package sqlite

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// TursoConfig configures the Turso platform backend. Org/Group/Prefix/APIToken
// drive provisioning; GroupToken is the database access token written into each
// shard's Target.
type TursoConfig struct {
	Org        string
	Group      string
	Prefix     string
	APIToken   string
	GroupToken string
}

// tursoProvisioner maps partitions to per-partition Turso databases named
// "<prefix>-<sanitized>". It caches name->target and tolerates the create
// already-exists race by re-fetching.
type tursoProvisioner struct {
	client tursoClient
	config TursoConfig

	mu    sync.Mutex
	cache map[string]Target
}

func newTursoProvisioner(client tursoClient, config TursoConfig) *tursoProvisioner {
	return &tursoProvisioner{client: client, config: config, cache: map[string]Target{}}
}

// databaseName builds the platform database name for a partition. The default
// partition uses the bare prefix.
func (p *tursoProvisioner) databaseName(name PartitionName) string {
	if name.IsDefault() {
		return p.config.Prefix
	}
	return p.config.Prefix + "-" + sanitizeTursoName(name.String())
}

func sanitizeTursoName(name string) string {
	// Turso database names allow lowercase alphanumerics and hyphens; map any
	// other byte to a hyphen so arbitrary partition-by names remain valid.
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c - 'A' + 'a')
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func (p *tursoProvisioner) targetFor(db tursoDatabase) Target {
	return Target{dsn: "libsql://" + db.Hostname, authToken: p.config.GroupToken}
}

func (p *tursoProvisioner) EnsureTarget(ctx context.Context, name PartitionName) (Target, error) {
	dbName := p.databaseName(name)

	p.mu.Lock()
	if tgt, ok := p.cache[dbName]; ok {
		p.mu.Unlock()
		return tgt, nil
	}
	p.mu.Unlock()

	db, _, err := p.client.CreateDatabase(ctx, p.config.Org, p.config.Group, dbName)
	if err != nil {
		return Target{}, fmt.Errorf("sqlite: failed to provision turso database %q: %w", dbName, err)
	}
	tgt := p.targetFor(db)

	p.mu.Lock()
	p.cache[dbName] = tgt
	p.mu.Unlock()
	return tgt, nil
}

func (p *tursoProvisioner) ExistingTarget(ctx context.Context, name PartitionName) (Target, bool, error) {
	dbName := p.databaseName(name)

	p.mu.Lock()
	if tgt, ok := p.cache[dbName]; ok {
		p.mu.Unlock()
		return tgt, true, nil
	}
	p.mu.Unlock()

	db, ok, err := p.client.GetDatabase(ctx, p.config.Org, dbName)
	if err != nil || !ok {
		return Target{}, false, err
	}
	tgt := p.targetFor(db)

	p.mu.Lock()
	p.cache[dbName] = tgt
	p.mu.Unlock()
	return tgt, true, nil
}

func (p *tursoProvisioner) NamedTargets(ctx context.Context) ([]NamedTarget, error) {
	databases, err := p.client.ListDatabases(ctx, p.config.Org)
	if err != nil {
		return nil, err
	}

	prefix := p.config.Prefix + "-"
	var named []NamedTarget
	for _, db := range databases {
		if db.Name != p.config.Prefix && !strings.HasPrefix(db.Name, prefix) {
			continue
		}
		named = append(named, NamedTarget{Name: db.Name, Target: p.targetFor(db)})
	}
	return named, nil
}
```

- [x] **Step 3c: Add the `Turso` constructor to `store.go`**

```go
// Turso builds a backend that provisions one Turso platform database per
// partition. Only naming strategies are legal.
func Turso(config TursoConfig, strategy NamingStrategy) Backend {
	provisioner := newTursoProvisioner(newHTTPTursoClient(config.APIToken), config)
	return Backend{strategy: strategy, catalog: newNamedTargetCatalog(strategy, provisioner)}
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./stores/sqlite/ -run TestTursoProvisioner -v && mise exec -- go build ./stores/sqlite/`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/turso-client.go stores/sqlite/turso-provisioner.go stores/sqlite/store.go stores/sqlite/turso-provisioner_test.go -m "Added the Turso platform client, provisioner, and Turso backend constructor"
```

---

## Task 13: Conformance matrix, shared-backing, and the concurrency regression test

**Files:**
- Modify: `stores/sqlite/store_test.go`
- Create: `stores/sqlite/conformance_test.go`, `stores/sqlite/concurrency_test.go`

- [x] **Step 1: Write the conformance matrix and stress test**

`conformance_test.go`:

```go
package sqlite

import (
	"context"
	"testing"

	"github.com/weegigs/wee-events-go/we"
)

// The validation suite runs against every local strategy: the partitioning
// layer must not change single-store semantics.
func TestValidationSuiteLocalStrategies(t *testing.T) {
	strategies := map[string]func() LocalStrategy{
		"global":       func() LocalStrategy { return Global() },
		"by_type":      func() LocalStrategy { return ByType() },
		"by_aggregate": func() LocalStrategy { return ByAggregate() },
		"hashed":       func() LocalStrategy { return Hashed(8) },
		"partition_by": func() LocalStrategy { return PartitionBy(func(id we.AggregateId) string { return id.Type }) },
	}
	for name, make := range strategies {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), make()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			we.NewEventStoreValidationSuite(ctx, store).Run(t)
		})
	}
}

// Shared-backing: two stores over one local file layout must observe each
// other's commits and enforce cross-instance revision conflicts per shard.
func TestSharedBackingLocalByType(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := NewStore(ctx, we.MakeJSONEncoder(), Local(dir, ByType()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewStore(ctx, we.MakeJSONEncoder(), Local(dir, ByType()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	we.NewSharedBackingSuite(ctx, first, second).Run(t)
}
```

`concurrency_test.go`:

```go
package sqlite

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

// TestConcurrentLoadPublishNoMisuse is the regression gate for the go-libsql
// SQLITE_MISUSE defect: many goroutines load and publish across shards with no
// failure. Run under -race in `just test`.
func TestConcurrentLoadPublishNoMisuse(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), Hashed(8)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := we.AggregateId{Type: "order", Key: fmt.Sprintf("agg-%d", w)}
			for i := 0; i < 20; i++ {
				if err := store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}); err != nil {
					errs <- err
					return
				}
				if _, err := store.Load(ctx, id); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}
```

- [x] **Step 2: Run to verify (these should PASS immediately — the implementation exists)**

Run: `mise exec -- go test ./stores/sqlite/ -run "ValidationSuiteLocal|SharedBackingLocal|ConcurrentLoadPublish" -race -v`
Expected: PASS across all strategies, no race, no `SQLITE_MISUSE`.

- [x] **Step 3: Commit**

```bash
jj split stores/sqlite/conformance_test.go stores/sqlite/concurrency_test.go -m "Added the per-strategy conformance matrix, shared-backing test, and the concurrency regression gate"
```

---

## Task 14: Per-strategy benchmarks and sqld/Turso integration tests

**Files:**
- Modify: `stores/sqlite/store_bench_test.go`
- Create: `stores/sqlite/sqld_integration_test.go`, `stores/sqlite/turso_live_test.go`

- [x] **Step 1: Rewrite the benchmark entry points**

```go
package sqlite

import (
	"context"
	"testing"

	"github.com/weegigs/wee-events-go/we"
)

func benchLocal(b *testing.B, strategy LocalStrategy) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(b.TempDir(), strategy))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

// BenchmarkSqliteInMemory measures the in-memory single-database store.
func BenchmarkSqliteInMemory(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

func BenchmarkSqliteLocalGlobal(b *testing.B)      { benchLocal(b, Global()) }
func BenchmarkSqliteLocalByType(b *testing.B)      { benchLocal(b, ByType()) }
func BenchmarkSqliteLocalByAggregate(b *testing.B) { benchLocal(b, ByAggregate()) }
func BenchmarkSqliteLocalHashed(b *testing.B)      { benchLocal(b, Hashed(8)) }
func BenchmarkSqliteLocalPartitionBy(b *testing.B) {
	benchLocal(b, PartitionBy(func(id we.AggregateId) string { return id.Type }))
}
```

- [x] **Step 2: Verify benchmarks compile and a leaf runs**

Run: `mise exec -- go test -run '^$' -bench 'BenchmarkSqliteLocalByType/creation/single$' -benchtime 1x ./stores/sqlite`
Expected: one PASS line, no failure.

- [x] **Step 3: Write the live Turso test (env-guarded)**

`turso_live_test.go`:

```go
package sqlite

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func tursoConfigFromEnv(t *testing.T) TursoConfig {
	t.Helper()
	cfg := TursoConfig{
		Org:        os.Getenv("TURSO_ORG"),
		Group:      os.Getenv("TURSO_GROUP"),
		Prefix:     os.Getenv("TURSO_DB_PREFIX"),
		APIToken:   os.Getenv("TURSO_API_TOKEN"),
		GroupToken: os.Getenv("TURSO_GROUP_TOKEN"),
	}
	if cfg.Org == "" || cfg.Group == "" || cfg.Prefix == "" || cfg.APIToken == "" || cfg.GroupToken == "" {
		t.Skip("TURSO_* environment not set; skipping live Turso test")
	}
	return cfg
}

// TestTursoLiveRoundTrip provisions a real prefixed database, round-trips and
// enumerates events, then deletes the database it created. Skipped unless the
// full TURSO_* set is present (available in the mise environment).
func TestTursoLiveRoundTrip(t *testing.T) {
	cfg := tursoConfigFromEnv(t)
	ctx := context.Background()

	store, err := NewStore(ctx, we.MakeJSONEncoder(), Turso(cfg, ByType()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	id := we.AggregateId{Type: "live-test", Key: "1"}
	t.Cleanup(func() {
		client := newHTTPTursoClient(cfg.APIToken)
		_ = client.DeleteDatabase(context.Background(), cfg.Org, cfg.Prefix+"-live-test")
	})

	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "live"}))

	agg, err := store.Load(ctx, id)
	require.NoError(t, err)
	require.Len(t, agg.Events, 1)

	ids, err := store.EnumerateAggregatesByType(ctx, "live-test")
	require.NoError(t, err)
	assert.Contains(t, ids, id)
}
```

`sqld_integration_test.go`: a placeholder is NOT acceptable; if a containerised sqld with the namespace admin API is available in the test environment, mirror `kurrent`'s testcontainer setup. If the test environment cannot run sqld with admin enabled, omit this file and note the gap in the final task's report rather than committing a skipped shell. Decision: **omit `sqld_integration_test.go`** in this plan; sqld-namespaced is covered by the named-catalog unit tests (Task 8) against the fake provisioner, and the live Turso test exercises the named-target path end to end. Record this as a known coverage boundary in the feature doc.

- [x] **Step 4: Run the live test (it will run with mise env, or skip without)**

Run: `mise exec -- go test ./stores/sqlite/ -run TestTursoLiveRoundTrip -v`
Expected: PASS (if `TURSO_*` present) or SKIP.

- [x] **Step 5: Commit**

```bash
jj split stores/sqlite/store_bench_test.go stores/sqlite/turso_live_test.go -m "Added per-strategy benchmarks and the env-guarded live Turso round-trip test"
```

---

## Task 15: ADR and feature documentation

**Files:**
- Create: `documents/adr/0013-sqlite-single-owner-shards.md`, `documents/features/10-sqlite-partitioning.md`
- Modify: `documents/adr/README.md`, `documents/roadmap.md`

- [x] **Step 1: Write ADR-0013**

```markdown
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
databases (ADR-0006). A later optimization — per-shard read pools for remote
backends — is left open and is benchmarkable via `just bench-compare`; it
would require a new ADR if it breaks the uniform model.

## Alternatives considered

- **Pin every pool to one connection, no sharding.** Fixes the defect but
  keeps the store single-writer globally and ignores the partitioning
  mandate.
- **Per-backend concurrency (single-owner local, pooled remote).** Buys remote
  read concurrency now at the cost of divergent semantics per backend and a
  Rust comparison that is no longer apples-to-apples.
- **Drop go-libsql.** No single pure-Go driver covers in-memory, local, and
  remote libSQL (ADR-0003); switching drivers is a larger, separate decision.
```

- [x] **Step 2: Add the index rows**

In `documents/adr/README.md`, after the 0012 row:

```markdown
| [0013](0013-sqlite-single-owner-shards.md) | SQLite store: single owner per shard, partitioned by strategy | Accepted |
```

In `documents/roadmap.md`, after the 0012 row:

```markdown
| [0013](adr/0013-sqlite-single-owner-shards.md) | SQLite store: single owner per shard, partitioned by strategy | Accepted |
```

- [x] **Step 3: Write the feature document**

```markdown
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
- SQLITE-P4.R1 — Each partition SHALL be owned by exactly one goroutine; no
  database SHALL be accessed from more than one goroutine (ADR-0013).
- SQLITE-P4.R2 — Concurrent loads and publishes across shards SHALL complete
  without `SQLITE_MISUSE`, verified under -race.
- SQLITE-P5.R1 — EnumerateAggregates and EnumerateAggregatesByType SHALL return
  the distinct aggregate ids across all known partitions, deduplicated.
- SQLITE-P6.R1 — The encoder SHALL remain an explicit constructor argument
  (ENCODING-S2); a nil encoder SHALL be a construction error.

## Coverage boundary

sqld-namespaced is verified through the named-catalog unit tests against a fake
provisioner and the named-target code path shared with Turso; a live sqld
container test is out of scope. The Turso platform path is verified end to end
by an env-guarded live test (TURSO_* in the mise environment).
```

- [x] **Step 4: Verify the docs build (links resolve, markdown valid)**

Run: `mise exec -- go build ./... && ls documents/adr/0013-sqlite-single-owner-shards.md documents/features/10-sqlite-partitioning.md`
Expected: both files listed, build clean.

- [x] **Step 5: Commit**

```bash
jj split documents/adr/0013-sqlite-single-owner-shards.md documents/adr/README.md documents/roadmap.md documents/features/10-sqlite-partitioning.md -m "Recorded ADR-0013 and feature 10 for the SQLite partitioning layer and single-owner-per-shard model"
```

---

## Task 16: Final verification

**Files:** none (verification only)

- [x] **Step 1: Full unit suite under race**

Run: `mise exec -- go test -race ./stores/sqlite/... ./we/...`
Expected: all PASS.

- [x] **Step 2: Lint and vet the whole module**

Run: `mise exec -- go vet ./... && mise exec -- golangci-lint run`
Expected: 0 issues.

- [x] **Step 3: Benchmark smoke — the regression gate**

Run: `mise exec -- go test -run '^$' -bench 'BenchmarkSqliteLocal' -benchtime 1x ./stores/sqlite 2>&1 | grep -E "FAIL|ns/op" | head -40`
Expected: every leaf shows `ns/op`, zero `FAIL` lines — the local-file concurrency defect is resolved across all strategies.

- [x] **Step 4: Confirm the old API is gone**

Run: `mise exec -- sh -c 'grep -rn "LocalFile(\|func InMemory()\|func Remote(\|func BusyTimeout(" stores/ samples/ || echo "old API removed"'`
Expected: `old API removed`.

- [x] **Step 5: Final commit if any formatting changed**

```bash
mise exec -- gofmt -s -w stores/sqlite/
jj split stores/sqlite/ -m "Applied gofmt simplification across the partitioning layer" # only if there is a diff
```

---

## Self-Review Notes

- **Spec coverage:** strategies (Tasks 2–3), backends/catalogs (Tasks 6–8, 12), single-owner shards (Task 9), routing/constructors (Task 10), enumeration (Task 11), Turso first-class (Task 12), conformance matrix + concurrency gate (Task 13), benchmarks + live Turso (Task 14), ADR + feature doc (Task 15), verification (Task 16). Document store and projections are excluded per the spec.
- **Greenness:** the old API survives until Task 10, where its last callers are migrated in the same commit. Tasks 1–9 add new files that compile alongside the old store (Task 4 moves `schema` but leaves `store.go` building).
- **Type consistency:** `Partition`, `Target`, `Backend`, `PartitionName`, `NamedTarget`, `ReadPlan`, `shard`, `Provisioner`, `tursoClient` names are used identically across tasks.
- **Known boundary:** no live sqld container test (Task 14) — named-target path covered by unit tests + live Turso; recorded in the feature doc.
- **Verification-before-completion:** Task 16 runs the full race suite, lint, and the benchmark regression gate before the work is called done.
```
