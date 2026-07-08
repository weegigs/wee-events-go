package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// sqldRegistry is the durable record of provisioned sqld namespaces. The sqld
// admin API cannot enumerate namespaces, so provisioning writes each
// (namespace, logical partition name) pair into a registry table in the
// always-present default-namespace database; existence checks and partition
// discovery read it back. Without this record a restarted process would treat
// every previously provisioned partition as absent — loads would silently
// return empty aggregates and enumeration would under-report.
//
// The default namespace is never a shard under a naming strategy (named
// strategies route every partition to a named namespace), and sqld arbitrates
// concurrent connections server-side, so the registry does not violate the
// single-owner-per-shard model (ADR-0013).
type sqldRegistry struct {
	target Target
}

const registrySchema = `CREATE TABLE IF NOT EXISTS _wee_events_partition_registry (
    namespace      TEXT PRIMARY KEY,
    partition_name TEXT NOT NULL
);`

// withDB opens the registry database, ensures the schema, and runs fn. The
// connection is per-operation: registry access is rare (provisioning and
// cache-miss lookups), which keeps the provisioner free of connection
// lifecycle.
func (r *sqldRegistry) withDB(ctx context.Context, fn func(db *sql.DB) error) error {
	dsn, err := targetDSN(r.target)
	if err != nil {
		return redactToken(err, r.target.authToken)
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return redactToken(fmt.Errorf("sqlite: failed to open partition registry: %w", err), r.target.authToken)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	err = withRemoteRouteRetry(ctx, isRemoteTarget(r.target.dsn), func() error {
		if _, err := db.ExecContext(ctx, registrySchema); err != nil {
			return fmt.Errorf("sqlite: failed to migrate partition registry: %w", err)
		}
		return fn(db)
	})
	return redactToken(err, r.target.authToken)
}

// record registers a namespace's logical partition name, idempotently. Two
// different partition names claiming one namespace is a naming collision:
// refusing it here keeps the second partition off the first one's database.
func (r *sqldRegistry) record(ctx context.Context, namespace, partition string) error {
	return r.withDB(ctx, func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO _wee_events_partition_registry (namespace, partition_name) VALUES (?, ?)`,
			namespace, partition,
		); err != nil {
			return fmt.Errorf("sqlite: failed to record partition registration: %w", err)
		}

		var stored string
		if err := db.QueryRowContext(ctx,
			`SELECT partition_name FROM _wee_events_partition_registry WHERE namespace = ?`, namespace,
		).Scan(&stored); err != nil {
			return fmt.Errorf("sqlite: failed to read partition registration: %w", err)
		}
		if stored != partition {
			return fmt.Errorf("sqlite: namespace collision: %q is registered for partition %q, cannot register %q",
				namespace, stored, partition)
		}
		return nil
	})
}

// lookup returns the logical partition name registered for a namespace.
func (r *sqldRegistry) lookup(ctx context.Context, namespace string) (string, bool, error) {
	var partition string
	found := false
	err := r.withDB(ctx, func(db *sql.DB) error {
		err := db.QueryRowContext(ctx,
			`SELECT partition_name FROM _wee_events_partition_registry WHERE namespace = ?`, namespace,
		).Scan(&partition)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("sqlite: failed to read partition registration: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return partition, found, nil
}

type registryEntry struct {
	namespace string
	partition string
}

// all returns every registered namespace ordered by namespace.
func (r *sqldRegistry) all(ctx context.Context) ([]registryEntry, error) {
	var entries []registryEntry
	err := r.withDB(ctx, func(db *sql.DB) error {
		rows, err := db.QueryContext(ctx,
			`SELECT namespace, partition_name FROM _wee_events_partition_registry ORDER BY namespace`)
		if err != nil {
			return fmt.Errorf("sqlite: failed to list partition registrations: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var e registryEntry
			if err := rows.Scan(&e.namespace, &e.partition); err != nil {
				return fmt.Errorf("sqlite: failed to scan partition registration: %w", err)
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}
