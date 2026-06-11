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
