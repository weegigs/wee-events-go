package main

import (
	"context"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/rs/zerolog/log"
	"github.com/weegigs/wee-events-go/connectors/wehttp"
	"github.com/weegigs/wee-events-go/we"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

func configureTracing() (func(), error) {
	exporter, err := we.HoneycombExporter(context.Background(), "a8204376beaa2c03a29cb7410379e00e", "counter")
	if err != nil {
		return nil, err
	}

	res, err := traceResource()
	if err != nil {
		return nil, err
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(res),
	)

	cleanup := func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Info().Err(err).Msg("tracing shutdown failed")
		}
	}

	otel.SetTracerProvider(tp)

	return cleanup, nil
}

func main() {

	tracingCleanup, err := configureTracing()
	if err != nil {
		log.Info().Err(err).Msg("failed to configure tracing")
		os.Exit(1)
	}
	defer tracingCleanup()

	service, serviceCleanup, err := local(context.Background())
	if err != nil {
		log.Info().Err(err).Msg("failed to configure service")
		os.Exit(1)
	}
	defer serviceCleanup()

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Mount("/", wehttp.NewHandler(service))

	addr := ":9080"
	log.Info().Str("addr", addr).Msg("starting server")

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Info().Err(err).Msg("server exited with error")
	}

}
