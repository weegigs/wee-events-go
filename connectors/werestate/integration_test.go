//go:build integration

// Integration test for the Restate connector. It is the ADR-0004 upgrade gate:
// it stands up a real Restate runtime (via testcontainers), registers the
// connector's virtual object through the SDK's HTTP endpoint, and proves handler
// registration plus idempotent / replay-safe execution against that runtime.
//
// The SDK ships its own testcontainers harness (github.com/restatedev/sdk-go/
// testing), but SDK v0.24.0 was built against testcontainers-go v0.40.0 and
// calls nat.Port.Int(); this repository pins testcontainers-go v0.42.0, whose
// MappedPort returns network.Port (which exposes .Port()/.Num(), not .Int()), so
// the SDK harness does not compile here. Rather than hold the repo-wide
// testcontainers pin back, this test drives the SDK's server.Restate handler
// through an equivalent harness written against the repo's testcontainers
// version. The compatibility gap is the kind of finding ADR-0004 designates the
// integration test to surface on an SDK bump.
//
// Requires Docker; gated behind the `integration` build tag and excluded from
// plain `just test`. Run with:
//
//	go test -tags integration -v ./connectors/werestate/
package werestate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/ingress"
	"github.com/restatedev/sdk-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weegigs/wee-events-go/samples/counter"
	"github.com/weegigs/wee-events-go/we"
)

const (
	serviceName  = "counter"
	adminPort    = "9070"
	ingressPort  = "8080"
	restateImage = "docker.io/restatedev/restate:latest"
)

func incrementCommand(t *testing.T, amount int) we.RemoteCommand {
	t.Helper()
	payload, err := json.Marshal(counter.Increment{Amount: amount})
	require.NoError(t, err)
	return we.RemoteCommand{
		CommandName: counter.IncrementCmd,
		Payload:     we.Data{Encoding: "application/json", Data: payload},
	}
}

// startEnvironment registers the connector's virtual object with a real Restate
// runtime and returns an ingress client plus the backing store. It mirrors the
// SDK's testing.StartWithOptions, written against testcontainers v0.42.0.
func startEnvironment(t *testing.T) (*ingress.Client, *memoryStore) {
	t.Helper()

	store := newMemoryStore()
	svc := NewService(counterService(store))

	restateSrv := server.NewRestate().Bind(svc.Definition(serviceName))
	restateHandler, err := restateSrv.Handler()
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(restateHandler)
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = &protocols
	srv.EnableHTTP2 = true
	srv.Start()
	t.Cleanup(srv.Close)

	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	sdkPort, err := strconv.Atoi(srvURL.Port())
	require.NoError(t, err)

	client := startRestateRuntime(t, sdkPort)
	return client, store
}

// startRestateRuntime runs the Restate container, registers the SDK endpoint
// listening on the host's sdkPort as a deployment, and returns an ingress
// client for it.
func startRestateRuntime(t *testing.T, sdkPort int) *ingress.Client {
	t.Helper()

	ctx := t.Context()
	restateC, err := testcontainers.Run(
		ctx, restateImage,
		testcontainers.WithEnv(map[string]string{
			"RESTATE_META__REST_ADDRESS":            "0.0.0.0:" + adminPort,
			"RESTATE_WORKER__INGRESS__BIND_ADDRESS": "0.0.0.0:" + ingressPort,
		}),
		testcontainers.WithExposedPorts(ingressPort+"/tcp", adminPort+"/tcp"),
		testcontainers.WithWaitStrategyAndDeadline(
			time.Minute,
			wait.ForAll(
				wait.ForHTTP("/health").WithPort(adminPort+"/tcp"),
				wait.ForHTTP("/restate/health").WithPort(ingressPort+"/tcp"),
			),
		),
		testcontainers.WithHostPortAccess(sdkPort),
	)
	testcontainers.CleanupContainer(t, restateC)
	require.NoError(t, err)

	mappedAdmin, err := restateC.MappedPort(ctx, adminPort)
	require.NoError(t, err)
	mappedIngress, err := restateC.MappedPort(ctx, ingressPort)
	require.NoError(t, err)

	// Register the SDK endpoint with the Restate runtime.
	body := fmt.Sprintf(`{"uri":"http://%s:%d"}`, testcontainers.HostInternal, sdkPort)
	res, err := http.Post(
		fmt.Sprintf("http://localhost:%s/deployments", mappedAdmin.Port()),
		"application/json", bytes.NewBufferString(body),
	)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusCreated, res.StatusCode)

	return ingress.NewClient(fmt.Sprintf("http://localhost:%s", mappedIngress.Port()))
}

// RESTATE-S2.R1 / RESTATE-S2.R3 — executing increment twice with the same
// idempotency key applies the command once; the replayed request returns the
// original result rather than appending new events.
func TestIdempotentExecute(t *testing.T) {
	client, store := startEnvironment(t)
	ctx := context.Background()

	key := "counter:idem-1"
	idempotencyKey := "increment-once"

	first, err := ingress.Object[we.RemoteCommand, map[string]any](client, serviceName, key, "execute").
		Request(ctx, incrementCommand(t, 5), restate.WithIdempotencyKey(idempotencyKey))
	require.NoError(t, err)
	assert.Equal(t, float64(5), first["current"])

	second, err := ingress.Object[we.RemoteCommand, map[string]any](client, serviceName, key, "execute").
		Request(ctx, incrementCommand(t, 5), restate.WithIdempotencyKey(idempotencyKey))
	require.NoError(t, err)

	// Same idempotency key: identical original result, no second application.
	assert.Equal(t, float64(5), second["current"], "replayed key must not re-apply the command")
	assert.Equal(t, first["$revision"], second["$revision"], "replay must return the original revision")

	// The store saw exactly one increment for the aggregate.
	id := we.AggregateId{Type: "counter", Key: "idem-1"}
	aggregate, err := store.Load(ctx, id)
	require.NoError(t, err)
	assert.Len(t, aggregate.Events, 1, "exactly one event applied for the idempotency key")
}

// RESTATE-S1.R1 / RESTATE-S1.R2 / RESTATE-S1.R3 — the registered object serves
// load and execute against a live runtime; execute advances state, load reads it.
func TestLoadAndExecuteThroughRuntime(t *testing.T) {
	client, _ := startEnvironment(t)
	ctx := context.Background()

	key := "counter:live-1"

	executed, err := ingress.Object[we.RemoteCommand, map[string]any](client, serviceName, key, "execute").
		Request(ctx, incrementCommand(t, 9), restate.WithIdempotencyKey("live-exec"))
	require.NoError(t, err)
	assert.Equal(t, float64(9), executed["current"])
	assert.Equal(t, "counter:live-1", executed["$id"])
	assert.Equal(t, "counter:counter", executed["$type"])

	loaded, err := ingress.Object[restate.Void, map[string]any](client, serviceName, key, "load").
		Request(ctx, restate.Void{}, restate.WithIdempotencyKey("live-load"))
	require.NoError(t, err)
	assert.Equal(t, float64(9), loaded["current"])
	assert.NotEmpty(t, loaded["$revision"])
}
