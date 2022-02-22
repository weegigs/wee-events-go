package we

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
)

func HttpHandler[T any](controller EntityService[T]) http.Handler {
	service := &httpService[T]{controller: controller, encoder: ResourceEncoder[T]{}}
	routes := httprouter.New()

	routes.HandlerFunc("GET", "/:type/:key", service.getResource())

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
