package wehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
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
	rejection := we.MakeRejection("bump.refused", "cannot bump in this state",
		map[string]we.ErrorField{"value": we.MakeI64Field(7)})
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
	rejection := we.MakeRejection("bump.refused", "no",
		map[string]we.ErrorField{"value": we.MakeI64Field(7)})
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
		{"GET with colon-bearing key", http.MethodGet, "/counter/with:colon"},
		{"GET with space-bearing key", http.MethodGet, "/counter/with%20space"},
		{"POST with colon-bearing type", http.MethodPost, "/with:colon/key1"},
		{"POST with space-bearing type", http.MethodPost, "/%20/key1"},
		{"POST with colon-bearing key", http.MethodPost, "/counter/with:colon"},
		{"POST with space-bearing key", http.MethodPost, "/counter/with%20space"},
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

// counterState is the aggregate state used by the wire-format tests.
type counterState struct {
	Value int `json:"value"`
}

// bump is the command type carried in the wire-format tests' payloads.
type bump struct {
	Amount int `json:"amount"`
}

// wireDecodingService executes a remote command through the framework's own
// payload decode path (we.CommandHandlerFunction.HandleRemoteCommand →
// we.UnmarshalFromData, dispatched on the payload's encoding tag) and mutates
// state, so a wire test proves the command decoded and executed end to end.
type wireDecodingService struct {
	state counterState
}

func (s *wireDecodingService) Load(context.Context, we.AggregateId) (we.Entity[counterState], error) {
	state := s.state
	return we.Entity[counterState]{
		Aggregate: we.AggregateId{Type: "counter", Key: "a"},
		Revision:  we.Revision("01HX0000000000000000000000"),
		Type:      "counter",
		State:     &state,
	}, nil
}

func (s *wireDecodingService) Execute(ctx context.Context, id we.AggregateId, cmd we.Command) (we.Entity[counterState], error) {
	remote, ok := cmd.(we.RemoteCommand)
	if !ok {
		return we.Entity[counterState]{}, errors.New("expected a remote command")
	}

	var handler we.CommandHandlerFunction[counterState, bump] = func(_ context.Context, cmd bump, _ we.Entity[counterState], _ we.EventPublisher) error {
		s.state.Value += cmd.Amount
		return nil
	}

	entity, err := s.Load(ctx, id)
	if err != nil {
		return we.Entity[counterState]{}, err
	}
	if err := handler.HandleRemoteCommand(ctx, remote, entity, nil); err != nil {
		return we.Entity[counterState]{}, err
	}

	return s.Load(ctx, id)
}

// postWire posts body as a command with the given wire media type.
func postWire(t *testing.T, handler http.Handler, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/counter/a", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// executeBumpOverJSONWire runs the JSON-wire equivalent of the CBOR-wire tests
// against a fresh service, returning the recorder as the behavioural baseline.
func executeBumpOverJSONWire(t *testing.T, payload we.Data) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(we.RemoteCommand{CommandName: "counter:bump", Payload: payload})
	require.NoError(t, err)

	service := &wireDecodingService{}
	rec := postWire(t, NewHandler[counterState](service), "application/json", body)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 3, service.state.Value)
	return rec
}

// ADR-0011 decision 5 — the wire format is edge-negotiated and CBOR is the
// encouraged wire. A POST with Content-Type: application/cbor whose body is a
// CBOR-marshalled RemoteCommand carrying a JSON-encoded payload (the normal
// case) executes exactly like the JSON-wire equivalent: same status, same
// state change, same JSON response.
func cborWireCarriesJSONEncodedPayload(t *testing.T) {
	payload, err := we.MakeJSONEncoder().Encode(bump{Amount: 3})
	require.NoError(t, err)

	body, err := cbor.Marshal(we.RemoteCommand{CommandName: "counter:bump", Payload: payload})
	require.NoError(t, err)

	service := &wireDecodingService{}
	rec := postWire(t, NewHandler[counterState](service), "application/cbor", body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 3, service.state.Value, "the command must execute and change state")

	baseline := executeBumpOverJSONWire(t, payload)
	assert.Equal(t, baseline.Code, rec.Code)
	assert.JSONEq(t, baseline.Body.String(), rec.Body.String(), "responses remain JSON regardless of the request wire")
}

// ADR-0011 decision 5 — the binary wire carries binary content natively end to
// end: a CBOR-wire RemoteCommand whose payload is CBOR-encoded (tagged
// application/cbor) decodes through the framework's tag-dispatched payload
// decoder (we.UnmarshalFromData routes application/cbor to the CBOR decoder)
// and executes exactly like the JSON-wire command carrying the same intent.
func cborWireCarriesCBOREncodedPayload(t *testing.T) {
	payload, err := we.MakeCBOREncoder().Encode(bump{Amount: 3})
	require.NoError(t, err)
	require.Equal(t, we.CBOREncoding, payload.Encoding)

	body, err := cbor.Marshal(we.RemoteCommand{CommandName: "counter:bump", Payload: payload})
	require.NoError(t, err)

	service := &wireDecodingService{}
	rec := postWire(t, NewHandler[counterState](service), "application/cbor", body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 3, service.state.Value, "the CBOR-tagged payload must decode and the command execute")

	jsonPayload, err := we.MakeJSONEncoder().Encode(bump{Amount: 3})
	require.NoError(t, err)
	baseline := executeBumpOverJSONWire(t, jsonPayload)
	assert.Equal(t, baseline.Code, rec.Code)
	assert.JSONEq(t, baseline.Body.String(), rec.Body.String(), "responses remain JSON regardless of the request wire")
}

// ADR-0011 decision 5 — the wire is negotiated over the supported media types
// only: an unsupported or malformed Content-Type is refused with a static 415
// and the entity service is never invoked.
func unsupportedWireMapsTo415(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
	}{
		{"unsupported media type", "text/plain"},
		{"malformed Content-Type header", ";;;"},
		{"absent Content-Type header", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewHandler[struct{}](invokeTrackingService{t: t})

			rec := postWire(t, handler, tc.contentType, []byte(`{}`))

			assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
			assert.Equal(t, "unsupported content type\n", rec.Body.String())
		})
	}
}

func TestCommandWireFormats(t *testing.T) {
	t.Run("CBOR wire carries a JSON-encoded payload (ADR-0011 decision 5)", cborWireCarriesJSONEncodedPayload)
	t.Run("CBOR wire carries a CBOR-encoded payload natively (ADR-0011 decision 5)", cborWireCarriesCBOREncodedPayload)
	t.Run("unsupported or malformed Content-Type maps to 415", unsupportedWireMapsTo415)
}

// responseCBORDecMode decodes CBOR response bodies into JSON-equivalent Go
// values (string-keyed maps via DefaultMapType) so a decoded CBOR body can be
// compared directly against json.Unmarshal of the JSON wire rendering. This
// is test-only comparison plumbing, not the hardened decode policy — that
// lives in we.HardenedCBORUnmarshal.
var responseCBORDecMode = mustCBORDecMode(cbor.DecOptions{
	DefaultMapType: reflect.TypeOf(map[string]any(nil)),
})

func mustCBORDecMode(opts cbor.DecOptions) cbor.DecMode {
	mode, err := opts.DecMode()
	if err != nil {
		panic(err)
	}
	return mode
}

func decodeCBORValue(t *testing.T, body []byte) any {
	t.Helper()
	var value any
	require.NoError(t, responseCBORDecMode.Unmarshal(body, &value))
	return normalizeCBORNumbers(value)
}

// normalizeCBORNumbers coerces CBOR's integer decode types (int64/uint64) to
// float64 so a decoded CBOR body is truly JSON-equivalent: json.Unmarshal
// renders every JSON number as float64, so a value-equality comparison against
// it must not trip over CBOR's distinct integer representation. (Rejection
// fields carry typed scalars — an I64 field encodes as a CBOR integer, a JSON
// number — so the two codecs' decoded types differ though the value is equal.)
func normalizeCBORNumbers(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = normalizeCBORNumbers(item)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = normalizeCBORNumbers(item)
		}
		return v
	case int64:
		return float64(v)
	case uint64:
		return float64(v)
	default:
		return value
	}
}

func decodeJSONValue(t *testing.T, body []byte) any {
	t.Helper()
	var value any
	require.NoError(t, json.Unmarshal(body, &value))
	return value
}

// getWithAccept performs a GET for /counter/a; an empty accept means the
// Accept header is absent.
func getWithAccept(t *testing.T, handler http.Handler, accept string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/counter/a", nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// postCommandWithAccept mirrors postCommand with an explicit Accept header.
func postCommandWithAccept(t *testing.T, handler http.Handler, accept string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(we.RemoteCommand{
		CommandName: "test:bump",
		Payload:     we.Data{Encoding: we.JSONEncoding, Data: json.RawMessage(`{}`)},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/counter/a", bytes.NewReader(body))
	req.Header.Set("Content-type", "application/json")
	req.Header.Set("Accept", accept)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// Test matrix "CBOR response" (Task 14b′; ADR-0011 decision 5) — a GET with
// Accept: application/cbor returns a CBOR body rendered from the same
// resource map as the JSON rendering: value-equal content, CBOR Content-Type.
func cborAcceptRendersGetResourceAsCBOR(t *testing.T) {
	service := &wireDecodingService{state: counterState{Value: 7}}
	handler := NewHandler[counterState](service)

	baseline := getWithAccept(t, handler, "")
	require.Equal(t, http.StatusOK, baseline.Code)
	require.Equal(t, "application/json", baseline.Header().Get("Content-Type"))

	rec := getWithAccept(t, handler, "application/cbor")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/cbor", rec.Header().Get("Content-Type"))
	assert.Equal(t, decodeJSONValue(t, baseline.Body.Bytes()), decodeCBORValue(t, rec.Body.Bytes()),
		"the CBOR body must be value-equal to the JSON rendering ($id/$type/$revision + state fields)")
}

// Task 14b′ — a command response negotiates the same way: a JSON-wire POST
// with Accept: application/cbor yields a CBOR resource body value-equal to the
// JSON-wire baseline response.
func cborAcceptRendersCommandResponseAsCBOR(t *testing.T) {
	payload, err := we.MakeJSONEncoder().Encode(bump{Amount: 3})
	require.NoError(t, err)

	baseline := executeBumpOverJSONWire(t, payload)

	body, err := json.Marshal(we.RemoteCommand{CommandName: "counter:bump", Payload: payload})
	require.NoError(t, err)

	service := &wireDecodingService{}
	req := httptest.NewRequest(http.MethodPost, "/counter/a", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/cbor")
	rec := httptest.NewRecorder()
	NewHandler[counterState](service).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 3, service.state.Value, "the command must still execute")
	assert.Equal(t, "application/cbor", rec.Header().Get("Content-Type"))
	assert.Equal(t, decodeJSONValue(t, baseline.Body.Bytes()), decodeCBORValue(t, rec.Body.Bytes()))
}

// Task 14b′ — the Accept scan is a media-range scan for an exact
// application/cbor item (q-values ignored); anything else keeps today's JSON
// default unchanged.
func responseWireNegotiationTable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		accept   string
		wantCBOR bool
	}{
		{"absent Accept stays JSON", "", false},
		{"application/json stays JSON", "application/json", false},
		{"*/* stays JSON", "*/*", false},
		{"application/* wildcard stays JSON", "application/*", false},
		{"unknown type stays JSON", "text/plain", false},
		{"exact application/cbor selects CBOR", "application/cbor", true},
		{"cbor among media ranges selects CBOR", "application/json, application/cbor", true},
		{"q-value on cbor is ignored", "application/cbor;q=0.1", true},
		{"malformed item is skipped and the scan continues", ";;;, application/cbor", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &wireDecodingService{state: counterState{Value: 7}}
			rec := getWithAccept(t, NewHandler[counterState](service), tc.accept)
			require.Equal(t, http.StatusOK, rec.Code)

			if tc.wantCBOR {
				assert.Equal(t, "application/cbor", rec.Header().Get("Content-Type"))
				return
			}
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			var resource map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resource), "the default body must remain a JSON document")
			assert.Equal(t, float64(7), resource["value"])
		})
	}
}

// Task 14b′ — the structured 422 rejection body is negotiated the same way:
// Accept: application/cbor renders the rejection's code, message, and context
// as a CBOR document value-equal to the JSON rendering.
func cborAcceptRendersRejectionBodyAsCBOR(t *testing.T) {
	rejection := we.MakeRejection("bump.refused", "cannot bump in this state",
		map[string]we.ErrorField{"value": we.MakeI64Field(7)})
	handler := NewHandler[struct{}](stubService{executeErr: rejection})

	baseline := postCommand(t, handler)
	require.Equal(t, http.StatusUnprocessableEntity, baseline.Code)

	rec := postCommandWithAccept(t, handler, "application/cbor")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "application/cbor", rec.Header().Get("Content-Type"))
	assert.Equal(t, decodeJSONValue(t, baseline.Body.Bytes()), decodeCBORValue(t, rec.Body.Bytes()),
		"the CBOR rejection body must be value-equal to the JSON rendering")
}

// Failure companion for the rejection rendering — a rejection whose context
// cannot be rendered (malformed JSON) maps to the static command-error 500
// before any status is committed, in both response media: never a partial
// 422, and no rejection or marshal-error detail reaches the body.
func rejectionMarshalFailureMapsToStatic5xx(t *testing.T) {
	rejection := we.MakeRejection("x", "y", map[string]we.ErrorField{"bad": {}})
	handler := NewHandler[struct{}](stubService{executeErr: rejection})

	t.Run("absent Accept renders the JSON path's static 500", func(t *testing.T) {
		rec := postCommand(t, handler)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, "failed to execute command\n", rec.Body.String())
	})

	t.Run("Accept application/cbor renders the CBOR path's static 500", func(t *testing.T) {
		rec := postCommandWithAccept(t, handler, "application/cbor")

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, "failed to execute command\n", rec.Body.String())
	})
}

// SURFACE-S4.R3 holds in both media — a resource that cannot be serialized
// yields the static 500 before any status is committed, even when the client
// asked for CBOR.
func cborAcceptEncodeFailureMapsToStatic5xx(t *testing.T) {
	handler := NewHandler[chan int](encodeFailStub{})

	req := httptest.NewRequest(http.MethodGet, "/counter/a", nil)
	req.Header.Set("Accept", "application/cbor")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "failed to encode resource\n", rec.Body.String())
}

func TestResponseWireNegotiation(t *testing.T) {
	t.Run("GET with Accept application/cbor renders CBOR (Task 14b′)", cborAcceptRendersGetResourceAsCBOR)
	t.Run("command response with Accept application/cbor renders CBOR", cborAcceptRendersCommandResponseAsCBOR)
	t.Run("Accept scan keeps JSON the default and finds exact cbor ranges", responseWireNegotiationTable)
	t.Run("rejection body negotiates the response wire", cborAcceptRendersRejectionBodyAsCBOR)
	t.Run("rejection marshal failure maps to the static 500 in both media", rejectionMarshalFailureMapsToStatic5xx)
	t.Run("encode failure under CBOR Accept maps to the static 500", cborAcceptEncodeFailureMapsToStatic5xx)
}

// paddedBumpCommand builds a JSON-wire RemoteCommand that is valid end to end
// (counter:bump, amount 3) padded with an ignored payload field so the encoded
// body is exactly size bytes. A test can therefore prove a request was refused
// purely for its size, not for being malformed.
func paddedBumpCommand(t *testing.T, size int) []byte {
	t.Helper()

	encode := func(pad int) []byte {
		payload := struct {
			Amount int    `json:"amount"`
			Pad    string `json:"pad"`
		}{Amount: 3, Pad: strings.Repeat("a", pad)}
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		body, err := json.Marshal(we.RemoteCommand{
			CommandName: "counter:bump",
			Payload:     we.Data{Encoding: we.JSONEncoding, Data: data},
		})
		require.NoError(t, err)
		return body
	}

	overhead := len(encode(0))
	require.GreaterOrEqual(t, size, overhead, "requested body size is smaller than the envelope overhead")
	body := encode(size - overhead)
	require.Len(t, body, size)
	return body
}

// Roadmap "wehttp request bodies are unbounded" — a command body over the
// default cap is refused at the wire edge with a static 413 before the body is
// parsed; the entity service is never invoked. The body is a valid command, so
// only the size cap can be refusing it.
func oversizedCommandBodyMapsToStatic413(t *testing.T) {
	handler := NewHandler[struct{}](invokeTrackingService{t: t})

	rec := postWire(t, handler, "application/json", paddedBumpCommand(t, defaultMaxBodyBytes+1))

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Equal(t, "request body too large\n", rec.Body.String())
}

// Companion bound check — a body exactly at the default cap passes the edge
// guard, parses, and executes.
func bodyAtCapStillExecutes(t *testing.T) {
	service := &wireDecodingService{}
	handler := NewHandler[counterState](service)

	rec := postWire(t, handler, "application/json", paddedBumpCommand(t, defaultMaxBodyBytes))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 3, service.state.Value, "an at-cap command must execute normally")
}

// MaxBodyBytes overrides the default cap per handler: a body over the
// configured cap is refused with the static 413, one within it executes.
func maxBodyBytesOptionOverridesDefault(t *testing.T) {
	const capBytes = 256

	t.Run("over the configured cap maps to 413", func(t *testing.T) {
		handler := NewHandler[struct{}](invokeTrackingService{t: t}, MaxBodyBytes[struct{}](capBytes))

		rec := postWire(t, handler, "application/json", paddedBumpCommand(t, capBytes+1))

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.Equal(t, "request body too large\n", rec.Body.String())
	})

	t.Run("within the configured cap executes", func(t *testing.T) {
		service := &wireDecodingService{}
		handler := NewHandler[counterState](service, MaxBodyBytes[counterState](capBytes))

		rec := postWire(t, handler, "application/json", paddedBumpCommand(t, capBytes))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 3, service.state.Value)
	})
}

func TestCommandBodySizeCap(t *testing.T) {
	t.Run("oversized body maps to a static 413", oversizedCommandBodyMapsToStatic413)
	t.Run("body at the default cap still executes", bodyAtCapStillExecutes)
	t.Run("MaxBodyBytes overrides the default cap", maxBodyBytesOptionOverridesDefault)
}

// invokeTrackingService fails the test if any service method is reached; it
// guards paths the boundary must reject before touching the service (invalid
// aggregate id, unsupported wire).
type invokeTrackingService struct{ t *testing.T }

func (s invokeTrackingService) Load(context.Context, we.AggregateId) (we.Entity[struct{}], error) {
	s.t.Fatal("Load must not be invoked for a request rejected at the boundary")
	return we.Entity[struct{}]{}, nil
}

func (s invokeTrackingService) Execute(context.Context, we.AggregateId, we.Command) (we.Entity[struct{}], error) {
	s.t.Fatal("Execute must not be invoked for a request rejected at the boundary")
	return we.Entity[struct{}]{}, nil
}
