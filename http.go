package we

import (
	"io"
	"mime"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/goccy/go-json"
	"github.com/julienschmidt/httprouter"
)

func HttpHandler[T any](controller EntityService[T]) http.Handler {
	service := &httpService[T]{controller: controller, encoder: ResourceEncoder[T]{}}
	routes := httprouter.New()

	routes.Handler("GET", "/:type/:key", WithOtel(service.getResource(), "get resource"))
	routes.Handler("POST", "/:type/:key", WithOtel(service.executeCommand(), "execute command"))

	return routes
}

type httpService[T any] struct {
	controller EntityService[T]
	encoder    EntityEncoder[T]
}

func (service *httpService[T]) getResource() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := httprouter.ParamsFromContext(r.Context())
		if len(params) == 0 {
			http.Error(w, "resource path not provided", http.StatusBadRequest)
			return
		}

		t := params.ByName("type")
		key := params.ByName("key")

		entity, err := service.controller.Load(r.Context(), AggregateId{Type: t, Key: key})
		if err != nil {
			log.Error(err)
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
		params := httprouter.ParamsFromContext(r.Context())
		if len(params) == 0 {
			http.Error(w, "resource path not provided", http.StatusBadRequest)
			return
		}

		t := params.ByName("type")
		key := params.ByName("key")

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

		var command RemoteCommand
		if err := json.Unmarshal(body, &command); err != nil {
			log.WithField("error", err).Info("failed to unmarshal command")
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		entity, err := service.controller.Execute(r.Context(), AggregateId{Type: t, Key: key}, command)
		if err != nil {
			log.Error(err)
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
