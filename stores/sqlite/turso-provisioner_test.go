package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestTursoProvisionerListsNamedTargetsAsLogicalNames(t *testing.T) {
	ctx := context.Background()
	client := newFakeTursoClient()
	prov := newTursoProvisioner(client, TursoConfig{Group: "g", Prefix: "we", GroupToken: "tok"})

	_, err := prov.EnsureTarget(ctx, PartitionName{name: "order"})
	require.NoError(t, err)
	_, err = prov.EnsureTarget(ctx, PartitionName{name: "user"})
	require.NoError(t, err)

	named, err := prov.NamedTargets(ctx)
	require.NoError(t, err)
	require.Len(t, named, 2)
	// NamedTargets must return the LOGICAL (strategy) names, not the prefixed
	// platform database names — the named-catalog wire-name fallback derives the
	// partition from NamedTarget.Name, so a prefixed name would yield the wrong
	// partition.
	names := []string{named[0].Name, named[1].Name}
	assert.Contains(t, names, "order")
	assert.Contains(t, names, "user")
}
