package main

import (
	"context"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	log "github.com/sirupsen/logrus"
	"github.com/weegigs/wee-events-go/connectors/wehttp"
	"github.com/weegigs/wee-events-go/we"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

func configureTracing() (func(), error) {
	exporter, err := we.JaegerExporter()
	if err != nil {
		return nil, err
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(traceResource()),
	)

	cleanup := func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.WithError(err).Info("tracing shutdown failed")
		}
	}

	otel.SetTracerProvider(tp)

	return cleanup, nil
}

func main() {

	traceingCleanup, err := configureTracing()
	if err != nil {
		log.WithError(err).Info("failed to configure tracing")
		os.Exit(1)
	}
	defer traceingCleanup()

	service, serviceCleanup, err := local(context.Background())
	if err != nil {
		log.WithError(err).Info("failed to configure service")
		os.Exit(1)
	}
	defer serviceCleanup()

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Mount("/", wehttp.NewHandler(service))

	addr := ":9080"
	log.WithField("addr", addr).Info("starting server")

	if err := http.ListenAndServe(addr, r); err != nil {
		log.WithError(err).Info("server exited with error")
	}

}
