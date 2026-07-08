package sqlite

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSqldProvisionerNamespaceAddressing(t *testing.T) {
	p := newSqldProvisioner("http://admin.local/", "libsql://data.local/", "tok")

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
	_, err := p.EnsureTarget(context.Background(), PartitionName{name: "order:abc_def"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/namespaces/order-abc-def/create", gotPath)

	tgt, ok, err := p.ExistingTarget(context.Background(), PartitionName{name: "order:abc_def"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "libsql://order-abc-def.data.local:8080", tgt.dsn)
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
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v1/namespaces/order/create", gotPath)
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "libsql://order.data.local", tgt.dsn)
}

func TestSqldProvisionerEnsureTargetSanitizesNamespacePath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order:abc_def"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/namespaces/order-abc-def/create", gotPath)
	assert.Equal(t, "libsql://order-abc-def.data.local", tgt.dsn)
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

func TestSqldProvisionerEnsureTargetToleratesAlreadyExistsBadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"namespace already exists"}`))
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "libsql://order.data.local", tgt.dsn)
}

func TestSqldProvisionerEnsureTargetRejectsErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := newSqldProvisioner(srv.URL, "libsql://data.local", "")
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
	tgt, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, "libsql://order.data.local", tgt.dsn)
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
	p.http.Transport = rt
	_, err := p.EnsureTarget(context.Background(), PartitionName{name: "order"})
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
}

func TestSqldProvisionerNamedTargetsEmpty(t *testing.T) {
	p := newSqldProvisioner("http://admin.local", "libsql://data.local", "tok")
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
	_, err := p.EnsureTarget(context.Background(), PartitionName{name: "order:abc"})
	require.NoError(t, err)

	named, err := p.NamedTargets(context.Background())
	require.NoError(t, err)
	require.Len(t, named, 1)
	assert.Equal(t, "order-abc", named[0].Name)
	assert.Equal(t, "libsql://order-abc.data.local", named[0].Target.dsn)
}
