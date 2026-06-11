package kurrent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weegigs/wee-events-go/we"
)

// TestClosedClientStaysPoisonedAfterRecovery PINS the defect recorded in
// documents/roadmap.md ("KurrentDB client does not recover a closed gRPC
// connection"). It asserts the CURRENT, broken behaviour of
// github.com/kurrent-io/KurrentDB-Client-Go v1.2.0:
//
//   - a transient Unavailable error triggers rediscovery on the next call;
//   - when discovery exhausts MaxDiscoverAttempts, the connection
//     state-machine goroutine sets a one-way close flag and exits
//     (kurrentdb/impl.go:240-247);
//   - afterwards EVERY operation on that client returns
//     ErrorCodeConnectionClosed permanently — even once the server is
//     healthy again. No API resets the flag; the only recovery is a new
//     kurrentdb.NewClient, and stores/kurrent holds one client per store
//     with no rebuild path.
//
// The test stops the server to poison the client, restarts the server,
// proves the server is healthy at the SAME address with a fresh client,
// and then asserts the poisoned store still fails with
// ErrorCodeConnectionClosed.
//
// When the store gains a recovery path (reconnect, or a typed
// connection-state error plus client rebuild), the final assertions flip
// from "still poisoned" to "recovered". Do NOT delete this harness — the
// stop/restart choreography is the regression test for the fix.
//
// Isolation: this test deliberately destroys a client, so it builds its
// OWN container and client rather than enrolling in NewKurrentTestStore.
// The container's host port is pinned (Docker reassigns ephemeral ports on
// restart) so "healthy again at the same address" is actually true.
func TestClosedClientStaysPoisonedAfterRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	hostPort := freeHostPort(t)

	ctr, err := testcontainers.Run(
		ctx,
		kurrentDBImage,
		testcontainers.WithEnv(map[string]string{
			"KURRENTDB_INSECURE": "true",
		}),
		testcontainers.WithExposedPorts("2113/tcp"),
		// Pin the host port: with the default ephemeral binding Docker may
		// allocate a DIFFERENT host port when the container restarts, which
		// would make the post-restart health check dishonest (the original
		// client's address would be dead regardless of the defect).
		testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.PortBindings = network.PortMap{
				network.MustParsePort("2113/tcp"): []network.PortBinding{
					{HostPort: strconv.Itoa(hostPort)},
				},
			}
		}),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("2113/tcp").WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		// Run returns the container alongside the error when create/start/wait
		// fails after the container exists (same pattern as NewKurrentTestStore).
		if ctr != nil {
			if terr := ctr.Terminate(context.Background()); terr != nil {
				t.Logf("failed to terminate container after startup error: %v", terr)
			}
		}
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// A fresh context: the test context may already be done, and the
		// poisoned client must not outlive the test in any form.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := ctr.Terminate(cleanupCtx); err != nil {
			t.Errorf("failed to terminate container: %v", err)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err)

	// A SMALL discovery window makes poisoning fast and deterministic:
	// 2 attempts x (fast connection-refused + 20ms interval) exhausts
	// discovery well under a second once the server is stopped.
	// gossiptimeout is in seconds; 1 is the smallest meaningful value.
	connection := fmt.Sprintf(
		"kurrentdb://admin:changeit@%s:%d?tls=false&maxdiscoverattempts=2&discoveryinterval=20&gossiptimeout=1",
		host, hostPort,
	)

	settings, err := kurrentdb.ParseConnectionString(connection)
	require.NoError(t, err)

	client, err := kurrentdb.NewClient(settings)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Logf("closing poisoned client: %v", err)
		}
	})

	store, err := NewEventStore(client, we.MakeJSONEncoder())
	require.NoError(t, err)

	id := we.AggregateId{Type: "reconnect", Key: "poisoned-client"}

	// Sanity: the store works while the server is up.
	require.NoError(t, store.Publish(ctx, id, we.Options(), TestEvent{Value: "before-outage"}))

	// Stop (not Terminate — the container must restart later with its data
	// and port binding intact).
	stopTimeout := 10 * time.Second
	require.NoError(t, ctr.Stop(ctx, &stopTimeout))

	// Drive the client into the poisoned state. Expected error sequence:
	// Unavailable (in-flight connection drops, schedules rediscovery) ->
	// discovery exhausts MaxDiscoverAttempts (state machine sets the close
	// flag and exits) -> ErrorCodeConnectionClosed forever after.
	var lastErr error
	attempts := 0
	poisoned := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		attempts++
		err := store.Publish(ctx, id, we.Options(), TestEvent{Value: "while-down"})
		if err != nil {
			lastErr = err
			var kErr *kurrentdb.Error
			if errors.As(err, &kErr) && kErr.Code() == kurrentdb.ErrorCodeConnectionClosed {
				poisoned = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !poisoned {
		t.Fatalf(
			"client did not reach ErrorCodeConnectionClosed within 30s of the server stopping (%d attempts); last error: %v",
			attempts, lastErr,
		)
	}
	t.Logf("client poisoned after %d attempts; poisoning error: %v", attempts, lastErr)

	// Restart the server. Start re-runs the wait strategy (readiedHook), so
	// the listening port is up again when it returns.
	require.NoError(t, ctr.Start(ctx))

	// Prove the server is genuinely healthy at the SAME address: a fresh
	// client over the identical connection string must publish successfully.
	// The port listens before the server fully accepts appends, so allow a
	// brief retry window.
	freshSettings, err := kurrentdb.ParseConnectionString(connection)
	require.NoError(t, err)
	freshClient, err := kurrentdb.NewClient(freshSettings)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := freshClient.Close(); err != nil {
			t.Logf("closing fresh client: %v", err)
		}
	})
	freshStore, err := NewEventStore(freshClient, we.MakeJSONEncoder())
	require.NoError(t, err)

	healthy := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		lastErr = freshStore.Publish(ctx, id, we.Options(), TestEvent{Value: "after-restart-fresh"})
		if lastErr == nil {
			healthy = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !healthy {
		t.Fatalf("restarted server never accepted a publish from a fresh client within 30s; last error: %v", lastErr)
	}

	// THE DEFECT: the original store still fails with
	// ErrorCodeConnectionClosed even though the server is healthy again.
	// Several attempts over a few seconds — the client must NOT recover.
	// When stores/kurrent gains recovery, flip these assertions to expect
	// success (and keep the choreography above).
	for attempt := range 5 {
		err := store.Publish(ctx, id, we.Options(), TestEvent{Value: "after-restart-poisoned"})
		require.Error(t, err,
			"attempt %d: poisoned client recovered — the roadmap defect appears fixed; flip this test to assert recovery", attempt)

		var kErr *kurrentdb.Error
		require.ErrorAs(t, err, &kErr, "attempt %d: expected a kurrentdb error, got: %v", attempt, err)
		assert.Equal(t, kurrentdb.ErrorCodeConnectionClosed, kErr.Code(),
			"attempt %d: expected ErrorCodeConnectionClosed, got code %d: %v", attempt, kErr.Code(), err)

		time.Sleep(500 * time.Millisecond)
	}

	// Reads are poisoned too — the close flag gates every operation.
	_, err = store.Load(ctx, id)
	require.Error(t, err, "poisoned client recovered on Load — flip this test to assert recovery")
	var kErr *kurrentdb.Error
	require.ErrorAs(t, err, &kErr)
	assert.Equal(t, kurrentdb.ErrorCodeConnectionClosed, kErr.Code())
}

// freeHostPort reserves an ephemeral TCP port and releases it so the
// container can bind it. The gap between release and bind is a benign race
// in a test context.
func freeHostPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}
