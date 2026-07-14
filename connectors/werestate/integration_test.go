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
	"errors"
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

	"github.com/weegigs/wee-events-go/samples/account"
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
func startEnvironment(t *testing.T) (*ingress.Client, string, *memoryStore) {
	t.Helper()

	store := newMemoryStore(we.MakeJSONEncoder())
	svc := NewService(counterService(store))

	accountSvc := NewService(account.Service(store))
	restateSrv := server.NewRestate().
		Bind(svc.Definition(serviceName)).
		Bind(accountSvc.Definition("account")).
		Bind(orchestratorDefinition())
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

	client, ingressURL := startRestateRuntime(t, sdkPort)
	return client, ingressURL, store
}

// startRestateRuntime runs the Restate container, registers the SDK endpoint
// listening on the host's sdkPort as a deployment, and returns an ingress
// client for it.
func startRestateRuntime(t *testing.T, sdkPort int) (*ingress.Client, string) {
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

	ingressURL := fmt.Sprintf("http://localhost:%s", mappedIngress.Port())
	return ingress.NewClient(ingressURL), ingressURL
}

// RESTATE-S2.R1 / RESTATE-S2.R3 — executing increment twice with the same
// idempotency key applies the command once; the replayed request returns the
// original result rather than appending new events.
func TestIdempotentExecute(t *testing.T) {
	client, _, store := startEnvironment(t)
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
	client, _, _ := startEnvironment(t)
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

// remoteCommand JSON-encodes a typed command into the RemoteCommand envelope.
func remoteCommand(t *testing.T, command any) we.RemoteCommand {
	t.Helper()
	payload, err := json.Marshal(command)
	require.NoError(t, err)
	return we.RemoteCommand{
		CommandName: we.CommandNameOf(command),
		Payload:     we.Data{Encoding: "application/json", Data: payload},
	}
}

// The full conformance loop for the error-frame contract: a domain rejection
// raised inside a handler crosses a real Restate runtime — mapError encodes
// the frame into the terminal message, the ingress carries it, and the typed
// boundary client decodes it back into a branchable we.Rejection with its
// fields intact. This is the cross-boundary coverage the 2026-07-08
// conformance review flagged as missing.
func TestRejectionRoundTripsAcrossBoundary(t *testing.T) {
	_, ingressURL, _ := startEnvironment(t)

	id, err := we.MakeAggregateId("account", "boundary-1")
	require.NoError(t, err)

	client := NewClient(ingressURL, "account")
	ctx := context.Background()

	_, err = client.Execute(ctx, id, remoteCommand(t, account.Open{Owner: "kevin"}))
	require.NoError(t, err, "opening the account must succeed")

	loaded, err := client.Load(ctx, id)
	require.NoError(t, err, "the typed client's load path must work against real ingress")
	assert.Equal(t, we.EncodedAggregateId("account:boundary-1"), loaded.ID)

	_, err = client.Execute(ctx, id, remoteCommand(t, account.Withdraw{Amount: 100}))

	var rejection we.Rejection
	require.True(t, errors.As(err, &rejection), "expected we.Rejection, got %T: %v", err, err)
	assert.Equal(t, "account.insufficient-funds", rejection.Code)

	balance, ok := rejection.Fields["balance"].I64()
	require.True(t, ok, "rejection must carry the balance field across the boundary")
	assert.Equal(t, int64(0), balance)

	requested, ok := rejection.Fields["requested"].I64()
	require.True(t, ok, "rejection must carry the requested field across the boundary")
	assert.Equal(t, int64(100), requested)

	var transport *TransportError
	assert.False(t, errors.As(err, &transport), "a declared rejection must not classify as transport")
}

// declaredReport is what the orchestrator hands back to the test: the
// classification result of an in-handler service-to-service failure.
type declaredReport struct {
	Declared     bool   `json:"declared"`
	Code         string `json:"code"`
	Balance      int64  `json:"balance"`
	HasBalance   bool   `json:"hasBalance"`
	Requested    int64  `json:"requested"`
	HasRequested bool   `json:"hasRequested"`
	RawMessage   string `json:"rawMessage"`
}

// orchestratorDefinition registers a plain Restate service whose handler
// calls the account virtual object INSIDE a handler context — the
// service-to-service lane, distinct from ingress — and classifies the
// failure with DeclaredError. It reports the classification as its success
// result so the raw propagated message survives for the test to inspect.
func orchestratorDefinition() restate.ServiceDefinition {
	return restate.NewService("orchestrator").
		Handler("overdraw", restate.NewServiceHandler(
			func(ctx restate.Context, accountKey string) (declaredReport, error) {
				open, err := json.Marshal(account.Open{Owner: "kevin"})
				if err != nil {
					return declaredReport{}, err
				}
				if _, err := restate.Object[EntityResponse](ctx, "account", accountKey, "execute").
					Request(we.RemoteCommand{
						CommandName: we.CommandNameOf(account.Open{}),
						Payload:     we.Data{Encoding: "application/json", Data: open},
					}); err != nil {
					return declaredReport{}, err
				}

				withdraw, err := json.Marshal(account.Withdraw{Amount: 100})
				if err != nil {
					return declaredReport{}, err
				}
				_, err = restate.Object[EntityResponse](ctx, "account", accountKey, "execute").
					Request(we.RemoteCommand{
						CommandName: we.CommandNameOf(account.Withdraw{}),
						Payload:     we.Data{Encoding: "application/json", Data: withdraw},
					})
				if err == nil {
					return declaredReport{}, restate.TerminalError(errors.New("overdraw unexpectedly succeeded"), http.StatusInternalServerError)
				}

				report := declaredReport{RawMessage: err.Error()}
				declared, ok := DeclaredError(err)
				report.Declared = ok
				if !ok {
					return report, nil
				}
				var rejection we.Rejection
				if errors.As(declared, &rejection) {
					report.Code = rejection.Code
					report.Balance, report.HasBalance = rejection.Fields["balance"].I64()
					report.Requested, report.HasRequested = rejection.Fields["requested"].I64()
				}
				return report, nil
			}))
}

// The service-to-service lane, end to end: a declared error raised inside
// service B's handler crosses the runtime to service A's in-handler call as
// a terminal error carrying the frame, and DeclaredError recovers it with
// its fields intact. RawMessage documents empirically what decoration the
// runtime applies on this path: the observed message is DOUBLY decorated —
// "[422] [422] wee-events:error-frame+json:{...}" — one "[<code>] " prefix
// per runtime/SDK hop (versus the single prefix on the ingress lane).
// DeclaredError strips leading decorations iteratively, so the frame-prefix
// assertion holds regardless of hop count.
func TestDeclaredErrorRecoversAcrossServiceToServiceCall(t *testing.T) {
	client, _, _ := startEnvironment(t)
	ctx := context.Background()

	report, err := ingress.Service[string, declaredReport](client, "orchestrator", "overdraw").
		Request(ctx, "account:orch-1")
	require.NoError(t, err, "the orchestrator handler itself must succeed")

	assert.True(t, report.Declared,
		"the in-handler failure must classify as declared; raw propagated message: %q", report.RawMessage)
	assert.Equal(t, "account.insufficient-funds", report.Code)
	assert.True(t, report.HasBalance, "the rejection must carry the balance field across the boundary")
	assert.Equal(t, int64(0), report.Balance)
	assert.True(t, report.HasRequested, "the rejection must carry the requested field across the boundary")
	assert.Equal(t, int64(100), report.Requested)
	assert.Contains(t, report.RawMessage, errorFramePrefix,
		"the propagated terminal message must carry the encoded frame")
}
