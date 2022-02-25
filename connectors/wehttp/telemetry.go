package wehttp

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func WithTelemetry(h http.Handler, name string) http.Handler {
	return otelhttp.NewHandler(h, name)
}
