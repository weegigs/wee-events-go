package sqlite

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRegistryTarget points a provisioner's partition registry at a local
// temporary file so tests never dial the default-namespace database.
func testRegistryTarget(t *testing.T) Target {
	t.Helper()
	return Target{dsn: "file:" + filepath.Join(t.TempDir(), "registry.db")}
}

func withTestRegistry(t *testing.T, p *sqldProvisioner) *sqldProvisioner {
	t.Helper()
	p.registry = &sqldRegistry{target: testRegistryTarget(t)}
	return p
}

func TestSqldProvisionerNamespaceAddressing(t *testing.T) {
	p := newSqldProvisioner("http://admin.local/", "libsql://data.local/", "tok")
	withTestRegistry(t, p)

	tgt, ok, err := p.ExistingTarget(context.Background(), PartitionName{isDefault: true})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "libsql://default.data.local", tgt.dsn)
	assert.Equal(t, "tok", tgt.authToken)

	_, ok, err = p.ExistingTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestSqldProvisionerNamespaceAddressingSanitizesNamesForHosts(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local:8080/", "tok")
	withTestRegistry(t, p)
	_, err := p.EnsureTarget(context.Background(), PartitionName{name: "order:abc_def"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/namespaces/order-abc-def-"+stableHashHex("order:abc_def")+"/create", gotPath)

	tgt, ok, err := p.ExistingTarget(context.Background(), PartitionName{name: "order:abc_def"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "libsql://order-abc-def-"+stableHashHex("order:abc_def")+".data.local:8080", tgt.dsn)
}

func TestSqldProvisionerEnsureTargetCreatesNamespace(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "tok")
	withTestRegistry(t, p)
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v1/namespaces/order-"+stableHashHex("order")+"/create", gotPath)
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "libsql://order-"+stableHashHex("order")+".data.local", tgt.dsn)
}

func TestSqldProvisionerEnsureTargetSanitizesNamespacePath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	withTestRegistry(t, p)
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order:abc_def"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/namespaces/order-abc-def-"+stableHashHex("order:abc_def")+"/create", gotPath)
	assert.Equal(t, "libsql://order-abc-def-"+stableHashHex("order:abc_def")+".data.local", tgt.dsn)
}

func TestSqldProvisionerEnsureTargetToleratesConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	withTestRegistry(t, p)
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "libsql://order-"+stableHashHex("order")+".data.local", tgt.dsn)
}

func TestSqldProvisionerEnsureTargetToleratesAlreadyExistsBadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"namespace already exists"}`))
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	withTestRegistry(t, p)
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "libsql://order-"+stableHashHex("order")+".data.local", tgt.dsn)
}

func TestSqldProvisionerEnsureTargetRejectsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	withTestRegistry(t, p)
	_, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sqlite:")
}

func TestSqldProvisionerEnsureTargetRetriesRetryableStatuses(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		switch attempts {
		case 1:
			w.WriteHeader(http.StatusInternalServerError)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	withTestRegistry(t, p)
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "libsql://order-"+stableHashHex("order")+".data.local", tgt.dsn)
	assert.Equal(t, 3, attempts)
}

func TestSqldProvisionerEnsureTargetDoesNotRetryPermanent4xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	withTestRegistry(t, p)
	_, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.Error(t, err)
	assert.Equal(t, 1, attempts)
}

func TestSqldProvisionerEnsureTargetRetriesTransportFailure(t *testing.T) {
	attempts := 0
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("temporary network failure")
		}
		return http.DefaultTransport.RoundTrip(req)
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	withTestRegistry(t, p)
	p.http.Transport = rt
	_, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
}

// The sqld admin API cannot enumerate namespaces, so provisioned partitions
// are recorded in a durable registry. A fresh provisioner (process restart)
// must see partitions provisioned by an earlier one, or reads silently return
// empty aggregates and enumeration under-reports.
func TestSqldProvisionerExistingTargetSurvivesRestart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	registry := testRegistryTarget(t)
	ctx := context.Background()

	first := newSqldProvisioner(srv.URL, "libsql://data.local", "tok")
	first.registry = &sqldRegistry{target: registry}
	_, err := first.EnsureTarget(ctx, PartitionName{name: "order:abc"})
	require.NoError(t, err)

	restarted := newSqldProvisioner(srv.URL, "libsql://data.local", "tok")
	restarted.registry = &sqldRegistry{target: registry}

	tgt, ok, err := restarted.ExistingTarget(ctx, PartitionName{name: "order:abc"})
	require.NoError(t, err)
	require.True(t, ok, "a restarted provisioner must find previously provisioned namespaces")
	assert.Equal(t, "libsql://order-abc-"+stableHashHex("order:abc")+".data.local", tgt.dsn)

	named, err := restarted.NamedTargets(ctx)
	require.NoError(t, err)
	require.Len(t, named, 1)
	assert.Equal(t, "order:abc", named[0].Name)
}

func TestSqldRegistryRecordIsIdempotentAndRejectsCollisions(t *testing.T) {
	ctx := context.Background()
	r := &sqldRegistry{target: testRegistryTarget(t)}

	require.NoError(t, r.record(ctx, "order-abc", "order:a.b"))
	require.NoError(t, r.record(ctx, "order-abc", "order:a.b"))

	err := r.record(ctx, "order-abc", "order:a_b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order:a.b")
	assert.Contains(t, err.Error(), "order:a_b")
}

func TestSqldProvisionerNamedTargetsEmpty(t *testing.T) {
	p := newSqldProvisioner("http://admin.local", "libsql://data.local", "tok")
	withTestRegistry(t, p)
	named, err := p.NamedTargets(context.Background())
	require.NoError(t, err)
	assert.Empty(t, named)
}

func TestSqldProvisionerNamedTargetsReportsKnownNamespaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "tok")
	withTestRegistry(t, p)
	_, err := p.EnsureTarget(context.Background(), PartitionName{name: "order:abc"})
	require.NoError(t, err)

	named, err := p.NamedTargets(context.Background())
	require.NoError(t, err)
	require.Len(t, named, 1)
	assert.Equal(t, "order:abc", named[0].Name)
	assert.Equal(t, "libsql://order-abc-"+stableHashHex("order:abc")+".data.local", named[0].Target.dsn)
}
