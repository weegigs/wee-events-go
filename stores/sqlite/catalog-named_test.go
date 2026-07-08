package sqlite

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProvisioner is an in-memory Provisioner for catalog tests.
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

	prov.targets["order"] = Target{dsn: "file:" + legacyPartitionDB(t, ctx, "order.db")}
	prov.targets["user"] = Target{dsn: "file:" + legacyPartitionDB(t, ctx, "user.db")}

	parts, err := cat.Partitions(ctx)
	require.NoError(t, err)
	require.Len(t, parts, 2)
	names := []string{parts[0].Name(), parts[1].Name()}
	sort.Strings(names)
	assert.Equal(t, []string{"order", "user"}, names)
}

func TestNamedCatalogPartitionsFallsBackForLegacyDatabaseWithoutMetadata(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvisioner()
	prov.targets["order"] = Target{dsn: "file:" + legacyPartitionDB(t, ctx, "legacy.db")}
	cat := newNamedTargetCatalog(ByType(), prov)

	parts, err := cat.Partitions(ctx)
	require.NoError(t, err)
	require.Equal(t, []Partition{MakePartition("order")}, parts)
}

func legacyPartitionDB(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	db, err := sql.Open(driverName, "file:"+path)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TABLE events (aggregate_type TEXT NOT NULL)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	return path
}

func TestNamedCatalogPartitionsSurfacesMetadataReadFailures(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvisioner()
	prov.targets["order"] = Target{dsn: "file:" + t.TempDir() + "/missing/partition.db"}
	cat := newNamedTargetCatalog(ByType(), prov)

	_, err := cat.Partitions(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sqlite:")
}

func TestNamedCatalogLogicalNameValidatesAuthenticatedTargetDSN(t *testing.T) {
	ctx := context.Background()
	prov := newFakeProvisioner()
	prov.targets["order"] = Target{dsn: "%", authToken: "secret"}
	cat := newNamedTargetCatalog(ByType(), prov)

	_, err := cat.Partitions(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid target dsn")
	assert.NotContains(t, err.Error(), "secret")
}
