package sqlite

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPTursoClientRetriesRetryableStatuses(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		switch attempts {
		case 1:
			w.WriteHeader(http.StatusInternalServerError)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"database":{"Name":"we-order","Hostname":"we-order.turso.io"}}`))
		}
	}))
	defer srv.Close()

	client := newHTTPTursoClient("api-token")
	client.baseURL = srv.URL

	db, existed, err := client.CreateDatabase(context.Background(), "org", "group", "we-order")
	require.NoError(t, err)
	assert.False(t, existed)
	assert.Equal(t, "we-order.turso.io", db.Hostname)
	assert.Equal(t, 3, attempts)
}

func TestHTTPTursoClientRetriesTransportFailure(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"database":{"Name":"we-order","Hostname":"we-order.turso.io"}}`))
	}))
	defer srv.Close()

	client := newHTTPTursoClient("api-token")
	client.baseURL = srv.URL
	client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("temporary network failure")
		}
		return http.DefaultTransport.RoundTrip(req)
	})

	_, _, err := client.CreateDatabase(context.Background(), "org", "group", "we-order")
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
}

func TestHTTPTursoClientDoesNotRetryNotFound(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := newHTTPTursoClient("api-token")
	client.baseURL = srv.URL

	_, ok, err := client.GetDatabase(context.Background(), "org", "missing")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, 1, attempts)
}

func TestHTTPTursoClientReusesConnectionAfterNotFound(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"database not found"}`))
	}))
	connections := 0
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections++
		}
	}
	srv.Start()
	defer srv.Close()

	client := newHTTPTursoClient("api-token")
	client.baseURL = srv.URL

	for range 2 {
		_, ok, err := client.GetDatabase(context.Background(), "org", "missing")
		require.NoError(t, err)
		assert.False(t, ok)
	}

	assert.Equal(t, 1, connections, "an undrained response body forces a new connection per request")
}

func TestHTTPTursoClientDoesNotRetryConflict(t *testing.T) {
	attemptsByPath := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptsByPath[r.URL.Path]++
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"database":{"Name":"we-order","Hostname":"we-order.turso.io"}}`))
	}))
	defer srv.Close()

	client := newHTTPTursoClient("api-token")
	client.baseURL = srv.URL

	db, ok, err := client.CreateDatabase(context.Background(), "org", "group", "we-order")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "we-order.turso.io", db.Hostname)
	assert.Equal(t, 1, attemptsByPath["/organizations/org/databases"])
	assert.Equal(t, 1, attemptsByPath["/organizations/org/databases/we-order"])
}
