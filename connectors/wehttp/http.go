package wehttp

import (
	"encoding/json"
	"errors"
	"fmt"
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

// defaultMaxBodyBytes caps how much of a command request body the intake will
// read. Commands are small envelopes — a command name plus an encoded payload,
// typically well under a kilobyte — so 1 MiB is generous headroom while still
// bounding what an unauthenticated client can make the server buffer.
const defaultMaxBodyBytes = 1 << 20

type HandlerOption[T any] func(service *httpService[T])

func Logger[T any](log *zerolog.Logger) HandlerOption[T] {
	return func(service *httpService[T]) {
		service.log = log
	}
}

// MaxBodyBytes overrides the default command-body cap (defaultMaxBodyBytes).
func MaxBodyBytes[T any](n int64) HandlerOption[T] {
	return func(service *httpService[T]) {
		service.maxBodyBytes = n
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
	if service.maxBodyBytes == 0 {
		service.maxBodyBytes = defaultMaxBodyBytes
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
	// maxBodyBytes bounds the command request body read off the wire.
	maxBodyBytes int64
	// encoder renders the resource per the negotiated response medium
	// (ADR-0011 decision 5): Marshal for JSON, MarshalCBOR for CBOR.
	encoder *we.ResourceEncoder[T]
}

// rejectionBody is the machine-readable payload returned for a domain rejection
// (REJECT-S2.R2). The context member is the rejection's fields flattened to
// plain JSON values: the tagged encoding belongs to the error-frame wire, not
// to this presentation edge (ADR-0011 decision 5).
type rejectionBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Context map[string]any `json:"context,omitempty"`
}

// flattenFields renders the closed scalar fields as plain JSON values. A
// zero-value field is a programmer error and fails the rendering (the caller
// maps that to a static 5xx rather than shipping invented content).
func flattenFields(fields map[string]we.ErrorField) (map[string]any, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	flat := make(map[string]any, len(fields))
	for name, field := range fields {
		if value, ok := field.Text(); ok {
			flat[name] = value
		} else if value, ok := field.I64(); ok {
			flat[name] = value
		} else if value, ok := field.U64(); ok {
			flat[name] = value
		} else if value, ok := field.Bool(); ok {
			flat[name] = value
		} else {
			return nil, fmt.Errorf("wehttp: rejection field %q has no value", name)
		}
	}
	return flat, nil
}

// marshalRejection renders the structured rejection body in the negotiated
// response wire (ADR-0011 decision 5) and returns the body with its media
// type. Both renderings are built from the same flattened scalar values.
func marshalRejection(rejection we.Rejection, asCBOR bool) ([]byte, string, error) {
	context, err := flattenFields(rejection.Fields)
	if err != nil {
		return nil, "", err
	}
	if !asCBOR {
		body, err := json.Marshal(rejectionBody{
			Code:    rejection.Code,
			Message: rejection.Message,
			Context: context,
		})
		return body, jsonWire, err
	}

	resource := map[string]any{
		"code":    rejection.Code,
		"message": rejection.Message,
	}
	if context != nil {
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
		// Marshal before committing the status so an unrenderable field (a
		// zero-value ErrorField) cannot leave a 422 with an empty body — fall
		// back to 500 if flattening or encoding fails.
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

		// The cap is edge protection at the wire layer — the public edge of the
		// connector. It bounds how many body bytes the intake will buffer and
		// has nothing to do with payload encodings, which ride inside the
		// envelope untouched. Exceeding it is a client fault: a static 413, not
		// a server error.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, service.maxBodyBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				service.log.Info().Err(err).Msg("rejected oversized command body")
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		var command we.RemoteCommand
		switch mediaType {
		case jsonWire:
			err = json.Unmarshal(body, &command)
		case cborWire:
			// Untrusted request bytes decode through the framework's single
			// hardened policy (we.HardenedCBORUnmarshal), never the unhardened
			// package-level cbor.Unmarshal.
			err = we.HardenedCBORUnmarshal(body, &command)
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
