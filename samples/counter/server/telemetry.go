package main

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

func traceResource() (*resource.Resource, error) {
	return mergeResources(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("counter"),
			semconv.ServiceVersion("v0.1.0"),
			attribute.String("environment", "development"),
		),
	)
}

func mergeResources(a, b *resource.Resource) (*resource.Resource, error) {
	merged, err := resource.Merge(a, b)
	if err != nil {
		// On schema conflict the SDK still returns a usable schemaless partial
		// resource; it is deliberately discarded here so tracing never starts
		// with a degraded identity (SURFACE-S4.R5).
		return nil, fmt.Errorf("failed to build trace resource: %w", err)
	}
	return merged, nil
}
