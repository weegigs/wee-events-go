package werestate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func clientAggregateId(t *testing.T) we.AggregateId {
	t.Helper()
	id, err := we.MakeAggregateId("counter", "client-1")
	require.NoError(t, err)
	return id
}

// entityBody is the ingress success payload: state flattened alongside the
// $-prefixed metadata, exactly what EntityResponse.MarshalJSON produces.
func entityBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(EntityResponse{
		State:    map[string]any{"current": float64(3)},
		ID:       we.EncodedAggregateId("counter:client-1"),
		Type:     we.EntityType("counter"),
		Revision: we.Revision("00000000000000000003"),
	})
	require.NoError(t, err)
	return body
}

func TestClientLoadDecodesEntityResponse(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(entityBody(t))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	response, err := client.Load(context.Background(), clientAggregateId(t))
	require.NoError(t, err)

	assert.Equal(t, "/counter/counter:client-1/load", gotPath)
	assert.Equal(t, we.EncodedAggregateId("counter:client-1"), response.ID)
	assert.Equal(t, map[string]any{"current": float64(3)}, response.State)
}

func TestClientExecutePostsCommandAndDecodesResponse(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(entityBody(t))
	}))
	defer server.Close()

	command := we.RemoteCommand{
		CommandName: "counter:increment",
		Payload:     we.Data{Encoding: "application/json", Data: []byte(`{"amount":1}`)},
	}

	client := NewClient(server.URL, "counter")
	response, err := client.Execute(context.Background(), clientAggregateId(t), command)
	require.NoError(t, err)

	var sent we.RemoteCommand
	require.NoError(t, json.Unmarshal(gotBody, &sent))
	assert.Equal(t, command.CommandName, sent.CommandName)
	assert.Equal(t, we.Revision("00000000000000000003"), response.Revision)
}

// A failure to reach the ingress at all is a transport failure with no status.
func TestClientConnectionFailureIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close() // deliberately dead

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
	assert.Equal(t, 0, transport.Status)
}

// A non-2xx response without a frame is a transport failure carrying the
// ingress status and message.
func TestClientPlainFailureIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"store is down","code":500}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
	assert.Equal(t, http.StatusInternalServerError, transport.Status)
	assert.Contains(t, transport.Message, "store is down")
}

// An undecodable success body is a transport failure too: the service answered
// but the boundary mangled it — never a declared outcome.
func TestClientUndecodableSuccessBodyIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"no":"metadata"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
}
