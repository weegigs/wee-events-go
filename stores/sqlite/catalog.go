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
