package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// SURFACE-S4.R5 — drift sentinel: the semconv version pinned in telemetry.go
// must stay schema-compatible with the SDK's resource.Default(). If this fails
// after an SDK upgrade, bump the semconv import in telemetry.go to match the
// version used by the SDK's resource package.
func TestTraceResourceSchemaIsCompatibleWithSDKDefault(t *testing.T) {
	_, err := traceResource()
	require.NoError(t, err)
}

// SURFACE-S4.R5 — the guard itself: merging conflicting schema URLs must
// error, not yield a partial resource.
func TestMergeResourcesRejectsSchemaConflict(t *testing.T) {
	conflicting, err := mergeResources(
		resource.NewWithAttributes("https://opentelemetry.io/schemas/1.20.0", semconv.ServiceName("a")),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("b")),
	)
	require.Error(t, err)
	assert.Nil(t, conflicting)
}
