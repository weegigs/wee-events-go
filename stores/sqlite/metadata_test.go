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
