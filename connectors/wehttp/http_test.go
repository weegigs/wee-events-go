package wehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

// stubService is a stub EntityService whose Execute returns a fixed error so a
// test can drive the connector's classification at the boundary.
type stubService struct {
	executeErr error
}

func (s stubService) Load(context.Context, we.AggregateId) (we.Entity[struct{}], error) {
	return we.Entity[struct{}]{}, errors.New("not used")
}

func (s stubService) Execute(context.Context, we.AggregateId, we.Command) (we.Entity[struct{}], error) {
	return we.Entity[struct{}]{}, s.executeErr
}

var _ we.EntityService[struct{}] = stubService{}

func postCommand(t *testing.T, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(we.RemoteCommand{
		CommandName: "test:bump",
		Payload:     we.Data{Encoding: we.JSONEncoding, Data: json.RawMessage(`{}`)},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/counter/a", bytes.NewReader(body))
	req.Header.Set("Content-type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// REJECT-S2.R1, REJECT-S2.R2 - a refused command yields a 4xx with a JSON body
// carrying the rejection's code, message, and context.
func rejectionMapsToStructured4xx(t *testing.T) {
	rejection := we.MakeRejection("bump.refused", "cannot bump in this state", json.RawMessage(`{"value":7}`))
	handler := NewHandler[struct{}](stubService{executeErr: rejection})

	rec := postCommand(t, handler)

	assert.GreaterOrEqual(t, rec.Code, 400)
	assert.Less(t, rec.Code, 500, "a rejection must be a 4xx, not a 5xx (REJECT-S2.R3)")

	var body struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Context json.RawMessage `json:"context"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "bump.refused", body.Code)
	assert.Equal(t, "cannot bump in this state", body.Message)
	assert.JSONEq(t, `{"value":7}`, string(body.Context))
}

// REJECT-S2.R1, REJECT-S2.R2 - a wrapped rejection is still recovered and
// mapped to a 4xx whose body carries the rejection's own code, message, and
// context — not the wrapper's text.
func wrappedRejectionMapsTo4xx(t *testing.T) {
	rejection := we.MakeRejection("bump.refused", "no", json.RawMessage(`{"value":7}`))
	wrapped := errors.Join(errors.New("execute command failed"), rejection)
	handler := NewHandler[struct{}](stubService{executeErr: wrapped})

	rec := postCommand(t, handler)

	assert.GreaterOrEqual(t, rec.Code, 400)
	assert.Less(t, rec.Code, 500)

	var body struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Context json.RawMessage `json:"context"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "bump.refused", body.Code)
	assert.Equal(t, "no", body.Message)
	assert.JSONEq(t, `{"value":7}`, string(body.Context))
	assert.NotContains(t, rec.Body.String(), "execute command failed", "wrapper text must not leak into the client body")
}

// REJECT-S3.R1, REJECT-S3.R2 - a store infrastructure error is a 5xx and never
// a 4xx rejection body.
func storeErrorMapsTo5xx(t *testing.T) {
	handler := NewHandler[struct{}](stubService{executeErr: errors.New("dynamodb is down")})

	rec := postCommand(t, handler)

	assert.GreaterOrEqual(t, rec.Code, 500)
}

// REJECT-S3.R2 - RevisionConflict is infrastructure-adjacent and maps to a 5xx,
// never a 4xx rejection body.
func revisionConflictMapsTo5xx(t *testing.T) {
	handler := NewHandler[struct{}](stubService{executeErr: we.RevisionConflict})

	rec := postCommand(t, handler)

	assert.GreaterOrEqual(t, rec.Code, 500)
}

// REJECT-S3.R1 (inbound decode) - an inbound-decode failure is a client error
// and maps to a 4xx, distinct from a store failure.
func decodeErrorMapsTo4xx(t *testing.T) {
	decodeErr := we.CommandDecodeFailed("test:bump", errors.New("malformed payload"))
	handler := NewHandler[struct{}](stubService{executeErr: decodeErr})

	rec := postCommand(t, handler)

	assert.GreaterOrEqual(t, rec.Code, 400)
	assert.Less(t, rec.Code, 500, "an inbound-decode failure is a client error (4xx)")
}

// An unknown command name is a client fault: it maps to 400 with a static
// body, matching the Restate connector's terminal classification (ADR-0005).
func commandNotFoundMapsTo400(t *testing.T) {
	notFound := we.CommandNotFound("test:bump")
	handler := NewHandler[struct{}](stubService{executeErr: notFound})

	rec := postCommand(t, handler)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "an unknown command is a client error, not a server fault")
	assert.Equal(t, "unknown command\n", rec.Body.String())
}

// encodeFailStub returns an initialized entity whose state cannot be JSON
// serialized, forcing the resource-encoding failure path.
type encodeFailStub struct{}

func (s encodeFailStub) Load(context.Context, we.AggregateId) (we.Entity[chan int], error) {
	ch := make(chan int)
	return we.Entity[chan int]{
		Aggregate: we.AggregateId{Type: "counter", Key: "a"},
		Revision:  we.Revision("01HX0000000000000000000000"),
		Type:      "counter",
		State:     &ch,
	}, nil
}

func (s encodeFailStub) Execute(ctx context.Context, id we.AggregateId, _ we.Command) (we.Entity[chan int], error) {
	return s.Load(ctx, id)
}

// A resource that cannot be serialized yields a 500 whose body is the static
// message only — the serializer's internal error text never reaches the client
// and the status is written exactly once.
func encodeFailureMapsToStatic5xx(t *testing.T) {
	handler := NewHandler[chan int](encodeFailStub{})

	req := httptest.NewRequest(http.MethodGet, "/counter/a", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "failed to encode resource\n", rec.Body.String())
}

func TestCommandErrorClassification(t *testing.T) {
	t.Run("rejection maps to structured 4xx (REJECT-S2.R1, REJECT-S2.R2)", rejectionMapsToStructured4xx)
	t.Run("wrapped rejection maps to 4xx (REJECT-S2.R1)", wrappedRejectionMapsTo4xx)
	t.Run("store error maps to 5xx (REJECT-S3.R1)", storeErrorMapsTo5xx)
	t.Run("revision conflict maps to 5xx (REJECT-S3.R2)", revisionConflictMapsTo5xx)
	t.Run("inbound-decode error maps to 4xx (REJECT-S3.R1)", decodeErrorMapsTo4xx)
	t.Run("unknown command maps to 400", commandNotFoundMapsTo400)
	t.Run("encode failure maps to a static 5xx", encodeFailureMapsToStatic5xx)
}

// IDENTITY-S3.R5 / R6 — invalid path identity is rejected at the boundary
// with a static 400; the entity service is never invoked. The POST cases
// deliberately carry no body and no Content-Type: identity validation must
// precede the media-type check, otherwise they would yield 415, not 400.
func TestInvalidAggregateIdMapsTo400(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{"GET with colon-bearing type", http.MethodGet, "/with:colon/key1"},
		{"GET with space-bearing type", http.MethodGet, "/%20/key1"},
		{"POST with colon-bearing type", http.MethodPost, "/with:colon/key1"},
		{"POST with space-bearing type", http.MethodPost, "/%20/key1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewHandler[struct{}](invokeTrackingService{t: t})

			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, "invalid aggregate id\n", rec.Body.String())
		})
	}
}

// SURFACE-S4.R4 — a body-write failure after the 200 is committed is logged
// and abandoned; the handler never writes a second status.
type failingWriter struct {
	header      http.Header
	statusCalls []int
}

func (w *failingWriter) Header() http.Header  { return w.header }
func (w *failingWriter) WriteHeader(code int) { w.statusCalls = append(w.statusCalls, code) }
func (w *failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestWriteFailureNeverWritesSecondStatus(t *testing.T) {
	handler := NewHandler[struct{}](loadableService{})
	w := &failingWriter{header: http.Header{}}
	req := httptest.NewRequest(http.MethodGet, "/counter/a", nil)

	handler.ServeHTTP(w, req)

	assert.Equal(t, []int{http.StatusOK}, w.statusCalls, "exactly one status write, the committed 200")
}

// loadableService returns a valid initialized entity.
type loadableService struct{}

func (s loadableService) Load(context.Context, we.AggregateId) (we.Entity[struct{}], error) {
	state := struct{}{}
	return we.Entity[struct{}]{
		Aggregate: we.AggregateId{Type: "counter", Key: "a"},
		Revision:  we.Revision("01HX0000000000000000000000"),
		Type:      "counter",
		State:     &state,
	}, nil
}

func (s loadableService) Execute(ctx context.Context, id we.AggregateId, _ we.Command) (we.Entity[struct{}], error) {
	return s.Load(ctx, id)
}

// invokeTrackingService fails the test if any service method is reached.
type invokeTrackingService struct{ t *testing.T }

func (s invokeTrackingService) Load(context.Context, we.AggregateId) (we.Entity[struct{}], error) {
	s.t.Fatal("Load must not be invoked for an invalid aggregate id")
	return we.Entity[struct{}]{}, nil
}

func (s invokeTrackingService) Execute(context.Context, we.AggregateId, we.Command) (we.Entity[struct{}], error) {
	s.t.Fatal("Execute must not be invoked for an invalid aggregate id")
	return we.Entity[struct{}]{}, nil
}
