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

func TestCommandErrorClassification(t *testing.T) {
	t.Run("rejection maps to structured 4xx (REJECT-S2.R1, REJECT-S2.R2)", rejectionMapsToStructured4xx)
	t.Run("wrapped rejection maps to 4xx (REJECT-S2.R1)", wrappedRejectionMapsTo4xx)
	t.Run("store error maps to 5xx (REJECT-S3.R1)", storeErrorMapsTo5xx)
	t.Run("revision conflict maps to 5xx (REJECT-S3.R2)", revisionConflictMapsTo5xx)
	t.Run("inbound-decode error maps to 4xx (REJECT-S3.R1)", decodeErrorMapsTo4xx)
}
