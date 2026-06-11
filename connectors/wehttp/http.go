package wehttp

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/fxamacker/cbor/v2"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"

	"github.com/weegigs/wee-events-go/we"
)

// Wire media types (ADR-0011 decision 5). These name the WIRE format — how
// the RemoteCommand envelope is spelled on the request body (Content-Type)
// and how responses are rendered (Accept) — not the payload's encoding tag,
// which rides inside the envelope untouched.
const (
	jsonWire = "application/json"
	cborWire = "application/cbor"
)

// commandCBORDecMode hardens CBOR decoding of untrusted request bodies,
// mirroring the decode-path policy in we/codec.go: duplicate map keys are
// rejected, indefinite-length items are forbidden, and nesting depth is
// capped. The unhardened package-level cbor.Unmarshal is never used here.
var commandCBORDecMode = mustCBORDecMode(cbor.DecOptions{
	DupMapKey:       cbor.DupMapKeyEnforcedAPF,
	IndefLength:     cbor.IndefLengthForbidden,
	MaxNestedLevels: 16,
})

func mustCBORDecMode(opts cbor.DecOptions) cbor.DecMode {
	mode, err := opts.DecMode()
	if err != nil {
		panic(err)
	}
	return mode
}

// acceptsCBOR scans the request's Accept media ranges for the CBOR wire.
// This negotiates the response WIRE format at the edge (ADR-0011 decision 5):
// responses are freshly rendered from the resource map in the negotiated
// medium, never transcoded from JSON text. Documented simplification (Task
// 14b′): each comma-separated item is parsed with mime.ParseMediaType, and an
// item whose parsed media type is application/cbor (case-insensitive;
// parameters, including q, are ignored) selects CBOR. Wildcard precedence is
// not implemented and unparseable items are skipped, so an absent header,
// */*, application/json, unknown, and malformed ranges all keep the JSON
// default.
func acceptsCBOR(r *http.Request) bool {
	for _, header := range r.Header.Values("Accept") {
		for item := range strings.SplitSeq(header, ",") {
			mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err == nil && mediaType == cborWire {
				return true
			}
		}
	}
	return false
}

type HandlerOption[T any] func(service *httpService[T])

func Logger[T any](log *zerolog.Logger) HandlerOption[T] {
	return func(service *httpService[T]) {
		service.log = log
	}
}

func NewHandler[T any](entityService we.EntityService[T], options ...HandlerOption[T]) http.Handler {
	service := &httpService[T]{controller: entityService, encoder: we.NewResourceEncoder[T]()}
	for _, option := range options {
		option(service)
	}
	if service.log == nil {
		service.log = &log.Logger
	}

	r := chi.NewRouter()

	r.Use(render.SetContentType(render.ContentTypeJSON))

	r.Method("GET", "/{type}/{key}", service.getResource())
	r.Method("POST", "/{type}/{key}", service.executeCommand())

	return otelhttp.NewHandler(r, "we-http")
}

type httpService[T any] struct {
	log        *zerolog.Logger
	controller we.EntityService[T]
	// encoder renders the resource per the negotiated response medium
	// (ADR-0011 decision 5): Marshal for JSON, MarshalCBOR for CBOR.
	encoder *we.ResourceEncoder[T]
}

// rejectionBody is the machine-readable payload returned for a domain rejection
// (REJECT-S2.R2). It mirrors we.Rejection's JSON shape.
type rejectionBody struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Context json.RawMessage `json:"context,omitempty"`
}

// marshalRejection renders the structured rejection body in the negotiated
// response wire (ADR-0011 decision 5) and returns the body with its media
// type. The CBOR rendering is built from the same structured values as the
// JSON one: the rejection's JSON context is decoded to a value and rendered
// natively — never embedded as opaque JSON text bytes.
func marshalRejection(rejection we.Rejection, asCBOR bool) ([]byte, string, error) {
	if !asCBOR {
		body, err := json.Marshal(rejectionBody{
			Code:    rejection.Code,
			Message: rejection.Message,
			Context: rejection.Context,
		})
		return body, jsonWire, err
	}

	resource := map[string]any{
		"code":    rejection.Code,
		"message": rejection.Message,
	}
	if len(rejection.Context) > 0 {
		var context any
		if err := json.Unmarshal(rejection.Context, &context); err != nil {
			return nil, "", err
		}
		resource["context"] = context
	}
	body, err := cbor.Marshal(resource)
	return body, cborWire, err
}

// writeCommandError classifies a command-path error at the edge (ADR-0005):
//
//   - a recovered we.Rejection is a domain refusal → 422 with a structured
//     body in the Accept-negotiated response wire (ADR-0011 decision 5)
//     carrying its code, message, and context (REJECT-S2.R1, REJECT-S2.R2);
//   - a we.DecodeError is an inbound client error (bad request) → 400
//     (REJECT-S3.R1, inbound decode);
//   - a we.CommandNotFoundError is a client addressing fault → 400, matching
//     the Restate connector's terminal classification (ADR-0005);
//   - everything else — store, codec, we.RevisionConflict, unexpected — is an
//     infrastructure fault → 500 and never a 4xx rejection body
//     (REJECT-S2.R3, REJECT-S3.R1, REJECT-S3.R2).
func (service *httpService[T]) writeCommandError(w http.ResponseWriter, r *http.Request, err error) {
	var rejection we.Rejection
	if errors.As(err, &rejection) {
		service.log.Info().Err(err).Str("code", rejection.Code).Msg("command rejected")
		// Marshal before committing the status so a malformed Context cannot
		// leave a 422 with an empty body — fall back to 500 if encoding fails.
		// The body is rendered in the Accept-negotiated wire; the plain-text
		// http.Error paths below are not negotiated.
		body, contentType, marshalErr := marshalRejection(rejection, acceptsCBOR(r))
		if marshalErr != nil {
			service.log.Info().Err(marshalErr).Msg("failed to encode rejection body")
			http.Error(w, "failed to execute command", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusUnprocessableEntity)
		if _, writeErr := w.Write(body); writeErr != nil {
			service.log.Info().Err(writeErr).Msg("failed to write rejection body")
		}
		return
	}

	var decode *we.DecodeError
	if errors.As(err, &decode) {
		service.log.Info().Err(err).Msg("rejected malformed command")
		http.Error(w, "invalid command payload", http.StatusBadRequest)
		return
	}

	var notFound we.CommandNotFoundError
	if errors.As(err, &notFound) {
		service.log.Info().Err(err).Msg("rejected unknown command")
		http.Error(w, "unknown command", http.StatusBadRequest)
		return
	}

	service.log.Info().Err(err).Msg("failed to execute command")
	http.Error(w, "failed to execute command", http.StatusInternalServerError)
}

func (service *httpService[T]) getResource() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := we.MakeAggregateId(chi.URLParam(r, "type"), chi.URLParam(r, "key"))
		if err != nil {
			service.log.Info().Err(err).Msg("rejected invalid aggregate id")
			http.Error(w, "invalid aggregate id", http.StatusBadRequest)
			return
		}

		entity, err := service.controller.Load(r.Context(), id)
		if err != nil {
			service.log.Info().Err(err).Str("type", id.Type).Str("key", id.Key).Msg("failed to load resource")
			http.Error(w, "failed to load resource", http.StatusInternalServerError)
			return
		}

		if !entity.Initialized() {
			http.NotFound(w, r)
			return
		}

		service.writeResource(w, r, entity)
	}
}

func (service *httpService[T]) executeCommand() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := we.MakeAggregateId(chi.URLParam(r, "type"), chi.URLParam(r, "key"))
		if err != nil {
			service.log.Info().Err(err).Msg("rejected invalid aggregate id")
			http.Error(w, "invalid aggregate id", http.StatusBadRequest)
			return
		}

		// This is the WIRE format, edge-negotiated via Content-Type (ADR-0011
		// decision 5): it selects how the RemoteCommand envelope is parsed off
		// the body. CBOR is the encouraged wire — every payload encoding rides
		// as native bytes — while the JSON wire uses we.Data's canonical JSON
		// spelling. The response wire is negotiated independently via Accept
		// (acceptsCBOR).
		contentType := r.Header.Get("Content-type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || (mediaType != jsonWire && mediaType != cborWire) {
			http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		var command we.RemoteCommand
		switch mediaType {
		case jsonWire:
			err = json.Unmarshal(body, &command)
		case cborWire:
			err = commandCBORDecMode.Unmarshal(body, &command)
		}
		if err != nil {
			service.log.Info().Err(err).Msg("failed to unmarshal command")
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		entity, err := service.controller.Execute(
			r.Context(),
			id,
			command,
		)
		if err != nil {
			service.writeCommandError(w, r, err)
			return
		}

		if !entity.Initialized() {
			http.NotFound(w, r)
			return
		}

		service.writeResource(w, r, entity)
	}
}

// writeResource marshals the entity in the Accept-negotiated response wire
// (ADR-0011 decision 5) and commits the response. The status is written
// exactly once, and only after marshalling succeeds: an encode failure maps
// to a static 500 before anything is committed, in both media
// (SURFACE-S4.R3).
func (service *httpService[T]) writeResource(w http.ResponseWriter, r *http.Request, entity we.Entity[T]) {
	marshal, contentType := service.encoder.Marshal, jsonWire
	if acceptsCBOR(r) {
		marshal, contentType = service.encoder.MarshalCBOR, cborWire
	}

	body, err := marshal(entity)
	if err != nil {
		service.log.Info().Err(err).Msg("failed to encode resource")
		http.Error(w, "failed to encode resource", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		// The status is committed; a write failure is a dead client, not a
		// recoverable response. Log and abandon (SURFACE-S4.R4).
		service.log.Info().Err(err).Msg("failed to write resource body")
	}
}
