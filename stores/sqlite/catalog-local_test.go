package sqlite

import (
	"context"
	"database/sql"
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
	// base32(NoPadding) of "order" is "N5ZGIZLS"; prefix "b32-".
	assert.Equal(t, "file:"+filepath.Join(dir, "b32-N5ZGIZLS.db"), tgt.dsn)
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

func openTarget(t *testing.T, tgt Target) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, tgt.dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	return db
}
