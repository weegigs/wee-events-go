package sqlite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSqldProvisionerNamespaceAddressing(t *testing.T) {
	p := newSqldProvisioner("http://admin.local/", "libsql://data.local/", "tok")

	tgt, ok, err := p.ExistingTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "libsql://order.data.local", tgt.dsn)
	assert.Equal(t, "tok", tgt.authToken)

	def, ok, err := p.ExistingTarget(context.Background(), PartitionName{isDefault: true})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "libsql://default.data.local", def.dsn)
}

func TestSqldProvisionerEnsureTargetCreatesNamespace(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "tok")
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/namespaces/order/create", gotPath)
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "libsql://order.data.local", tgt.dsn)
}

func TestSqldProvisionerEnsureTargetToleratesConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "libsql://order.data.local", tgt.dsn)
}

func TestSqldProvisionerNamedTargetsEmpty(t *testing.T) {
	p := newSqldProvisioner("http://admin.local", "libsql://data.local", "tok")
	named, err := p.NamedTargets(context.Background())
	require.NoError(t, err)
	assert.Empty(t, named)
}
