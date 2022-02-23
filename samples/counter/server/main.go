package main

import (
	"context"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	we "github.com/weegigs/wee-events-go"
)

func run() error {
	service, cleanup, err := local(context.Background())
	if err != nil {
		log.Fatalf("failed to configure controller: %v", err)
	}
	defer cleanup()

	handler := we.HttpHandler(service)

	addr := ":9080"
	log.WithField("addr", addr).Info("starting server")
	return http.ListenAndServe(addr, WithLogging(handler))
}

func main() {
	if err := run(); err != nil {
		log.WithField("event", "start server").Fatal(err)
	}
}

func WithLogging(h http.Handler) http.Handler {
	logFn := func(rw http.ResponseWriter, r *http.Request) {
		start := time.Now()

		uri := r.RequestURI
		method := r.Method
		h.ServeHTTP(rw, r) // serve the original request

		duration := time.Since(start)

		// log request details
		log.WithFields(log.Fields{
			"uri":      uri,
			"method":   method,
			"duration": duration,
		}).Info()
	}
	return http.HandlerFunc(logFn)
}
