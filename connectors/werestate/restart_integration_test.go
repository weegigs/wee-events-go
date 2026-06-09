//go:build integration

// Service kill/restart integration test (RESTATE-S2.R2, RESTATE-S2.R4): the
// SDK endpoint is killed while an invocation is mid-dispatch and restarted on
// the same address; the runtime must drive the command to exactly-once
// completion, and once the dispatch outcome is journaled, a service restart
// plus replay must yield the journaled result without re-running the dispatch.
package werestate

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/ingress"
	"github.com/restatedev/sdk-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

// gatedStore wraps an EventStore so the test can observe and control the first
// dispatch: the first Publish signals entry and then blocks until released,
// honouring context cancellation so a publish whose invocation died with the
// service is aborted rather than applied. Later publishes pass straight
// through. The attempt counter distinguishes a journal replay (no new attempt)
// from a re-dispatch.
type gatedStore struct {
	we.EventStore

	mu       sync.Mutex
	attempts int

	enterOnce sync.Once
	entered   chan struct{}
	release   chan struct{}
}

func newGatedStore(backing we.EventStore) *gatedStore {
	return &gatedStore{
		EventStore: backing,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (g *gatedStore) Publish(ctx context.Context, id we.AggregateId, options we.PublishOptions, events ...we.DomainEvent) error {
	g.mu.Lock()
	g.attempts++
	first := g.attempts == 1
	g.mu.Unlock()

	if first {
		g.enterOnce.Do(func() { close(g.entered) })
		select {
		case <-g.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		// The gate is only released after the service hosting this invocation
		// has been killed; an invocation whose connection died must not apply
		// its effects (the runtime will re-dispatch on the restarted service).
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	return g.EventStore.Publish(ctx, id, options, events...)
}

func (g *gatedStore) publishAttempts() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.attempts
}

// restartableEndpoint serves the SDK handler over unencrypted HTTP/2 on a fixed
// host port so the endpoint can be killed and brought back at the address the
// Restate runtime has registered.
type restartableEndpoint struct {
	t       *testing.T
	handler http.Handler
	addr    string
	srv     *http.Server
}

func newRestartableEndpoint(t *testing.T, handler http.Handler) *restartableEndpoint {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	e := &restartableEndpoint{t: t, handler: handler, addr: listener.Addr().String()}
	e.serve(listener)
	t.Cleanup(e.stop)
	return e
}

func (e *restartableEndpoint) serve(listener net.Listener) {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{Handler: e.handler, Protocols: &protocols}
	e.srv = srv
	go func() { _ = srv.Serve(listener) }()
}

func (e *restartableEndpoint) port() int {
	_, port, err := net.SplitHostPort(e.addr)
	require.NoError(e.t, err)
	n, err := strconv.Atoi(port)
	require.NoError(e.t, err)
	return n
}

// stop kills the endpoint hard: the listener and every active connection —
// including in-flight invocation streams — are closed.
func (e *restartableEndpoint) stop() {
	if e.srv != nil {
		_ = e.srv.Close()
		e.srv = nil
	}
}

// restart brings the endpoint back on the same address.
func (e *restartableEndpoint) restart() {
	e.t.Helper()

	var listener net.Listener
	require.Eventually(e.t, func() bool {
		l, err := net.Listen("tcp", e.addr)
		if err != nil {
			return false
		}
		listener = l
		return true
	}, 10*time.Second, 50*time.Millisecond, "rebind %s", e.addr)

	e.serve(listener)
}

// RESTATE-S2.R2 / RESTATE-S2.R4 — kill and restart the service mid-invocation:
// the command completes exactly once, and a post-completion service restart
// plus idempotent replay serves the journaled result without re-running the
// dispatch.
func TestServiceRestartMidInvocation(t *testing.T) {
	backing := newMemoryStore()
	store := newGatedStore(backing)
	svc := NewService(counterService(store))

	restateSrv := server.NewRestate().Bind(svc.Definition(serviceName))
	restateHandler, err := restateSrv.Handler()
	require.NoError(t, err)

	endpoint := newRestartableEndpoint(t, restateHandler)
	client := startRestateRuntime(t, endpoint.port())
	ctx := context.Background()

	const key = "counter:restart-1"
	const idempotencyKey = "increment-across-restart"

	type outcome struct {
		response map[string]any
		err      error
	}
	results := make(chan outcome, 1)
	go func() {
		response, reqErr := ingress.Object[we.RemoteCommand, map[string]any](client, serviceName, key, "execute").
			Request(ctx, incrementCommand(t, 7), restate.WithIdempotencyKey(idempotencyKey))
		results <- outcome{response: response, err: reqErr}
	}()

	// Wait until the invocation is mid-dispatch, then kill the service before
	// the dispatch outcome can be journaled (RESTATE-S2.R2: process dies
	// mid-invocation).
	select {
	case <-store.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("invocation never reached the store")
	}
	endpoint.stop()
	close(store.release)
	endpoint.restart()

	// The runtime retries against the restarted service and completes the
	// command exactly once.
	var first outcome
	select {
	case first = <-results:
	case <-time.After(2 * time.Minute):
		t.Fatal("invocation did not complete after service restart")
	}
	require.NoError(t, first.err)
	assert.Equal(t, float64(7), first.response["current"])

	id := we.AggregateId{Type: "counter", Key: "restart-1"}
	aggregate, err := backing.Load(ctx, id)
	require.NoError(t, err)
	require.Len(t, aggregate.Events, 1, "command must be applied exactly once across the restart")

	attemptsAfterCompletion := store.publishAttempts()
	require.GreaterOrEqual(t, attemptsAfterCompletion, 2, "the killed dispatch must have been re-driven by the runtime")

	// RESTATE-S2.R4: the outcome is now journaled. Kill and restart the service
	// again, then replay the same idempotency key — the journaled result must
	// be served without the dispatch re-running.
	endpoint.stop()
	endpoint.restart()

	replayed, err := ingress.Object[we.RemoteCommand, map[string]any](client, serviceName, key, "execute").
		Request(ctx, incrementCommand(t, 7), restate.WithIdempotencyKey(idempotencyKey))
	require.NoError(t, err)
	assert.Equal(t, first.response["current"], replayed["current"])
	assert.Equal(t, first.response["$revision"], replayed["$revision"])

	assert.Equal(t, attemptsAfterCompletion, store.publishAttempts(), "journaled replay must not re-run the dispatch")

	aggregate, err = backing.Load(ctx, id)
	require.NoError(t, err)
	assert.Len(t, aggregate.Events, 1, "journaled side effects must not re-run")
}
