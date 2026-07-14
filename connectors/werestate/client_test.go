package werestate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

	var rejection we.Rejection
	assert.False(t, errors.As(err, &rejection), "a transport failure must never classify as a declared rejection")

	_, declared := DeclaredError(err)
	assert.False(t, declared, "a transport failure must not be claimed by DeclaredError")
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

	var rejection we.Rejection
	assert.False(t, errors.As(err, &rejection), "a transport failure must never classify as a declared rejection")

	_, declared := DeclaredError(err)
	assert.False(t, declared, "a transport failure must not be claimed by DeclaredError")
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

// framedFailureBody builds the exact ingress failure body the werestate server
// produces: mapError encodes the rejection's frame into the terminal message,
// and the ingress renders {"message": <terminal message>, "code": <status>}.
func framedFailureBody(t *testing.T, rejection we.Rejection, status int) []byte {
	t.Helper()
	message, err := encodeErrorFrame(rejection.ToErrorFrame())
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{"message": message, "code": status})
	require.NoError(t, err)
	return body
}

// The declared lane: a framed 422 decodes back into a branchable we.Rejection
// with its fields intact — the same value a caller would see in-process.
func TestClientDecodesFramedRejection(t *testing.T) {
	rejection := we.MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]we.ErrorField{
			"balance":   we.MakeI64Field(0),
			"requested": we.MakeI64Field(100),
		})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(framedFailureBody(t, rejection, http.StatusUnprocessableEntity))
	}))
	defer server.Close()

	client := NewClient(server.URL, "account")
	_, err := client.Execute(context.Background(), clientAggregateId(t), we.RemoteCommand{})

	var recovered we.Rejection
	require.True(t, errors.As(err, &recovered), "expected we.Rejection, got %T: %v", err, err)
	assert.Equal(t, rejection, recovered)

	var transport *TransportError
	assert.False(t, errors.As(err, &transport), "a declared error must never read as a transport failure")
}

// The real Restate ingress decorates a terminal error's message with a
// "[<code>] " prefix before rendering the failure body — the form observed at
// the live boundary in TestRejectionRoundTripsAcrossBoundary. The client must
// strip that transport artifact before frame decode so the declared rejection
// still comes back with its fields intact.
func TestClientDecodesDecoratedFramedRejection(t *testing.T) {
	rejection := we.MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]we.ErrorField{
			"balance":   we.MakeI64Field(0),
			"requested": we.MakeI64Field(100),
		})

	message, err := encodeErrorFrame(rejection.ToErrorFrame())
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"message": "[422] " + message,
		"code":    http.StatusUnprocessableEntity,
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	client := NewClient(server.URL, "account")
	_, err = client.Execute(context.Background(), clientAggregateId(t), we.RemoteCommand{})

	var recovered we.Rejection
	require.True(t, errors.As(err, &recovered), "expected we.Rejection, got %T: %v", err, err)
	assert.Equal(t, rejection, recovered)

	var transport *TransportError
	assert.False(t, errors.As(err, &transport), "a declared error must never read as a transport failure")
}

// A decorated NON-frame message stays in the transport lane, and the
// TransportError preserves the original decorated message — stripping is only
// an aid to frame decode, never a rewrite of transport diagnostics.
func TestClientDecoratedPlainFailureStaysTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"[500] store is down","code":500}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
	assert.Equal(t, http.StatusInternalServerError, transport.Status)
	assert.Equal(t, "[500] store is down", transport.Message, "the transport lane must keep the decorated original")

	var rejection we.Rejection
	assert.False(t, errors.As(err, &rejection), "a transport failure must never classify as a declared rejection")

	_, declared := DeclaredError(err)
	assert.False(t, declared, "a transport failure must not be claimed by DeclaredError")
}

// insufficientFundsError is a service-specific declared error a caller might
// define; the decoder test proves callers can branch on their own types via
// errors.As rather than the generic rejection.
type insufficientFundsError struct {
	Balance   int64
	Requested int64
}

func (e *insufficientFundsError) Error() string {
	return "insufficient funds"
}

// A registered FrameDecoder claims frames it recognises, so callers branch on
// their own declared error types rather than the generic rejection.
func TestClientCustomDecoderClaimsFrame(t *testing.T) {
	rejection := we.MakeRejection("account.insufficient-funds", "insufficient funds",
		map[string]we.ErrorField{
			"balance":   we.MakeI64Field(25),
			"requested": we.MakeI64Field(100),
		})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(framedFailureBody(t, rejection, http.StatusUnprocessableEntity))
	}))
	defer server.Close()

	client := NewClient(server.URL, "account", Decoder(func(frame we.ErrorFrame) (error, bool) {
		if frame.Code != "account.insufficient-funds" {
			return nil, false
		}
		balance, _ := frame.Fields["balance"].I64()
		requested, _ := frame.Fields["requested"].I64()
		return &insufficientFundsError{Balance: balance, Requested: requested}, true
	}))

	_, err := client.Execute(context.Background(), clientAggregateId(t), we.RemoteCommand{})

	var declared *insufficientFundsError
	require.True(t, errors.As(err, &declared), "expected *insufficientFundsError, got %T: %v", err, err)
	assert.Equal(t, int64(25), declared.Balance)
	assert.Equal(t, int64(100), declared.Requested)
}

// A decoder that declines passes through to the generic rejection fallback.
func TestClientUnclaimedFrameFallsBackToRejection(t *testing.T) {
	rejection := we.MakeRejection("order.closed", "order is closed", nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(framedFailureBody(t, rejection, http.StatusUnprocessableEntity))
	}))
	defer server.Close()

	client := NewClient(server.URL, "order", Decoder(func(we.ErrorFrame) (error, bool) {
		return nil, false
	}))

	_, err := client.Execute(context.Background(), clientAggregateId(t), we.RemoteCommand{})

	var recovered we.Rejection
	require.True(t, errors.As(err, &recovered))
	assert.Equal(t, "order.closed", recovered.Code)
}

// A buggy decoder that claims a frame but returns a nil error must be treated as
// unclaimed: the call still fails, falling through to the generic rejection
// fallback rather than surfacing a non-2xx response as an empty success.
func TestClientNilClaimedDecoderFallsBackToRejection(t *testing.T) {
	rejection := we.MakeRejection("order.closed", "order is closed", nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(framedFailureBody(t, rejection, http.StatusUnprocessableEntity))
	}))
	defer server.Close()

	client := NewClient(server.URL, "order", Decoder(func(we.ErrorFrame) (error, bool) {
		return nil, true
	}))

	_, err := client.Execute(context.Background(), clientAggregateId(t), we.RemoteCommand{})

	require.Error(t, err, "a nil claimed decoder result must never surface as a successful entity")

	var recovered we.Rejection
	require.True(t, errors.As(err, &recovered), "expected we.Rejection, got %T: %v", err, err)
	assert.Equal(t, "order.closed", recovered.Code)
}

// Every ingress request declares the response format it can decode.
func TestClientSendsAcceptHeader(t *testing.T) {
	var gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(entityBody(t))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))
	require.NoError(t, err)

	assert.Equal(t, "application/json", gotAccept)
}

// paddedEntityBody returns a valid entity body padded with a filler state
// field to exactly size bytes. The filler is plain ASCII so the JSON length
// grows byte-for-byte with the filler content.
func paddedEntityBody(t *testing.T, size int) []byte {
	t.Helper()
	build := func(filler string) []byte {
		body, err := json.Marshal(EntityResponse{
			State:    map[string]any{"filler": filler},
			ID:       we.EncodedAggregateId("counter:client-1"),
			Type:     we.EntityType("counter"),
			Revision: we.Revision("00000000000000000003"),
		})
		require.NoError(t, err)
		return body
	}
	base := build("")
	require.LessOrEqual(t, len(base), size, "cap too small to build a padded body")
	padded := build(strings.Repeat("x", size-len(base)))
	require.Len(t, padded, size)
	return padded
}

// A response body over the cap is a transport failure — never a truncated,
// half-decoded value.
func TestClientOversizedBodyIsTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(paddedEntityBody(t, maxResponseBytes+1))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	_, err := client.Load(context.Background(), clientAggregateId(t))

	var transport *TransportError
	require.True(t, errors.As(err, &transport), "expected *TransportError, got %T: %v", err, err)
	assert.Contains(t, transport.Message, "response body exceeds")
	assert.Equal(t, http.StatusOK, transport.Status, "the httptest server answered 200 with an oversized body")
}

// A body exactly at the cap still decodes: the bound is a ceiling, not an
// off-by-one truncation.
func TestClientBodyAtCapStillDecodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(paddedEntityBody(t, maxResponseBytes))
	}))
	defer server.Close()

	client := NewClient(server.URL, "counter")
	response, err := client.Load(context.Background(), clientAggregateId(t))
	require.NoError(t, err)
	assert.Equal(t, we.EncodedAggregateId("counter:client-1"), response.ID)
}
