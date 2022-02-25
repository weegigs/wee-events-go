package wehttp

import (
	"io"
	"mime"
	"net/http"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/goccy/go-json"

	"github.com/weegigs/wee-events-go/we"
)

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
	encoder    we.EntityEncoder[T]
}

func (service *httpService[T]) getResource() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := chi.URLParam(r, "type")
		key := chi.URLParam(r, "key")

		entity, err := service.controller.Load(r.Context(), we.AggregateId{Type: t, Key: key})
		if err != nil {
			service.log.Info().Err(err).Str("type", t).Str("key", key).Msg("failed to load resource")
			http.Error(w, "failed to load resource", http.StatusInternalServerError)
			return
		}

		if !entity.Initialized() {
			http.NotFound(w, r)
			return
		}

		service.encoder.Encode(w, r, entity)
	}
}

func (service *httpService[T]) executeCommand() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := chi.URLParam(r, "type")
		key := chi.URLParam(r, "key")

		contentType := r.Header.Get("Content-type")
		mediaType, _, err := mime.ParseMediaType(contentType)
		if mediaType != "application/json" || err != nil {
			http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		var command we.RemoteCommand
		if err := json.UnmarshalContext(r.Context(), body, &command); err != nil {
			log.Info().Err(err).Msg("failed to unmarshal command")
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		entity, err := service.controller.Execute(r.Context(), we.AggregateId{Type: t, Key: key}, command)
		if err != nil {
			log.Info().Err(err).Msg("failed to execute command")
			http.Error(w, "failed to execute command", http.StatusInternalServerError)
			return
		}

		if !entity.Initialized() {
			http.NotFound(w, r)
			return
		}

		service.encoder.Encode(w, r, entity)
	}
}
