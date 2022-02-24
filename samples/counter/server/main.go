package main

import (
	"context"
	"io"
	"net/http"
	"os"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	we "github.com/weegigs/wee-events-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

func run() error {

	service, cleanup, err := local(context.Background())
	if err != nil {
		log.WithError(err).Info("failed to configure controller")
		return errors.Wrap(err, "failed to configure controller")
	}
	defer cleanup()

	handler := we.HttpHandler(service)

	addr := ":9080"
	log.WithField("addr", addr).Info("starting server")
	return http.ListenAndServe(addr, withLogging(handler))
}

func main() {
	exporter, err := we.ConsoleExporter(io.Discard)
	if err != nil {
		log.WithError(err).Info("failed to configure controller")
		os.Exit(1)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(traceResource()),
	)

	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.WithError(err).Info("tracing shutdown failed")
		}
	}()

	otel.SetTracerProvider(tp)

	if err := run(); err != nil {
		log.WithError(err).Info("server exited with error")
	}
}
