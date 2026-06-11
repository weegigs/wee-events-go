package kurrent

import (
	"context"
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

// TestStoreRecoversAfterServerOutage pins the store's transparent recovery
// from a poisoned KurrentDB client (owner decision recorded in
// documents/roadmap.md, "KurrentDB client does not recover a closed gRPC
// connection": recovery is rebuilt inside the store, invisible to callers).
//
// The underlying defect in github.com/kurrent-io/KurrentDB-Client-Go v1.2.0:
// when rediscovery exhausts MaxDiscoverAttempts the connection state-machine
// goroutine sets a one-way close flag and exits (kurrentdb/impl.go:240-247),
// after which EVERY operation on that client returns
// ErrorCodeConnectionClosed permanently — even once the server is healthy
// again. No API resets the flag; the only recovery is kurrentdb.NewClient.
//
// The store therefore detects ErrorCodeConnectionClosed and rebuilds a fresh
// client from the retained configuration. Reads and exact-expectation appends
// are then retried once; blind appends (Any{} — what this test publishes) are
// NOT retried, because the error can also be mapped post-dispatch and a retry
// could silently duplicate a committed append: the rebuilt client serves the
// NEXT call instead. This test pins that behaviour end to end:
//
//   - operations while the server is down still FAIL (recovery must never
//     mask a still-down server — errors during an outage are errors);
//   - after the server restarts on the SAME address, operations on the SAME
//     store succeed again (the first post-restart publish consumes the
//     poisoned client and rebuilds; being a blind append it still returns the
//     error, and the next attempt succeeds on the rebuilt client — hence the
//     retry loop below);
//   - a second outage drives the client into a KNOWN poisoned state, and a
//     single exact-expectation publish then succeeds WITHIN ONE CALL — the
//     deterministic pin on the duplicate-safe retry branch itself.
//
// The stop/restart choreography is the regression test for the fix — keep it.
//
// Isolation: this test deliberately poisons clients, so it builds its OWN
// container and store rather than enrolling in NewKurrentTestStore. The
// container's host port is pinned (Docker reassigns ephemeral ports on
// restart) so "healthy again at the same address" is actually true.
func TestStoreRecoversAfterServerOutage(t *testing.T) {
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
		// would make the post-restart recovery check dishonest (the original
		// client's address would be dead regardless of recovery).
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
		// container must not outlive the test in any form.
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

	store, err := NewEventStore(settings, we.MakeJSONEncoder())
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Logf("closing store: %v", err)
		}
	})

	id := we.AggregateId{Type: "reconnect", Key: "server-outage"}

	// Sanity: the store works while the server is up.
	require.NoError(t, store.Publish(ctx, id, we.Options(), TestEvent{Value: "before-outage"}))

	// Stop (not Terminate — the container must restart later with its data
	// and port binding intact).
	stopTimeout := 10 * time.Second
	require.NoError(t, ctr.Stop(ctx, &stopTimeout))

	// Drive publishes while the server is down. Every one must FAIL —
	// recovery is about the NEXT call succeeding once the server is healthy,
	// never about masking a still-down server. Ten attempts comfortably
	// exhausts the 2-attempt discovery window, so when the server comes back
	// the store's current client is poisoned or freshly rebuilt — both
	// states the recovery loop must handle.
	for attempt := range 10 {
		err := store.Publish(ctx, id, we.Options(), TestEvent{Value: "while-down"})
		require.Error(t, err,
			"attempt %d: publish succeeded while the server was down — recovery must not mask an outage", attempt)
		t.Logf("publish while down (attempt %d): %v", attempt, err)
		time.Sleep(100 * time.Millisecond)
	}

	// Restart the server. Start re-runs the wait strategy (readiedHook), so
	// the listening port is up again when it returns.
	require.NoError(t, ctr.Start(ctx))

	// Prove the server is genuinely healthy at the SAME address: a fresh
	// client over the identical connection string must publish successfully.
	// Without this, a recovery failure below could be a still-sick server
	// rather than the store. The port listens before the server fully
	// accepts appends, so allow a brief retry window.
	freshSettings, err := kurrentdb.ParseConnectionString(connection)
	require.NoError(t, err)
	freshStore, err := NewEventStore(freshSettings, we.MakeJSONEncoder())
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := freshStore.Close(); err != nil {
			t.Logf("closing fresh store: %v", err)
		}
	})

	var lastErr error
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

	// RECOVERY: the SAME store must succeed once the server is healthy again.
	// Allow a few attempts — these are blind appends (Any{}), so the first
	// post-restart publish consumes the poisoned client and rebuilds but
	// still returns the error (no duplicate-unsafe retry); the next attempt
	// succeeds on the rebuilt client.
	recovered := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		lastErr = store.Publish(ctx, id, we.Options(), TestEvent{Value: "after-restart-recovered"})
		if lastErr == nil {
			recovered = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !recovered {
		t.Fatalf("store did not recover within 30s of the server restarting; last error: %v", lastErr)
	}

	// Steady state: once recovered, the store stays recovered.
	for attempt := range 3 {
		require.NoError(t, store.Publish(ctx, id, we.Options(), TestEvent{Value: "after-recovery-steady"}),
			"attempt %d: store failed again after recovering", attempt)
	}

	// Reads recover too — the poison gated every operation, so recovery must
	// cover the read path as well.
	loaded, err := store.Load(ctx, id)
	require.NoError(t, err, "Load failed on the recovered store")
	// before-outage + after-restart-fresh + after-restart-recovered + 3
	// steady-state publishes; >= guards against a timed-out publish that
	// nonetheless persisted server-side.
	assert.GreaterOrEqual(t, len(loaded.Events), 6)
	var first TestEvent
	require.NoError(t, we.UnmarshalFromData(loaded.Events[0].Data, &first))
	assert.Equal(t, "before-outage", first.Value, "pre-outage data must survive the outage")

	// THE EXACT-EXPECTATION RETRY PIN: a single publish carrying an exact
	// expected revision must succeed WITHIN ONE CALL against a poisoned
	// client. This assertion fails if the duplicate-safe retry branch in
	// Publish is removed or inverted — without the retry the call returns
	// the poisoned client's error instead of succeeding.
	//
	// Determinism of "the client is poisoned when the pin runs": the test is
	// in-package, so it poisons the store's CURRENT client directly —
	// driving raw appends on store.client() while the server is stopped,
	// bypassing Publish and therefore independent of any mutation to its
	// recovery logic. The loop runs until the raw client itself returns
	// ErrorCodeConnectionClosed, which proves the one-way close flag is set
	// (kurrentdb v1.2.0 impl.go:240-247); no store call happens in between,
	// so the store still holds exactly that poisoned client when the pin
	// runs. The probe targets a scratch stream so the target stream's
	// revision cannot move.
	stopTimeout = 10 * time.Second
	require.NoError(t, ctr.Stop(ctx, &stopTimeout))

	raw := store.client()
	probe := kurrentdb.EventData{
		ContentType: kurrentdb.ContentTypeJson,
		EventType:   "poison-probe",
		Data:        []byte(`{}`),
	}
	poisoned := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		_, lastErr = raw.AppendToStream(ctx, "reconnect-poison-probe", kurrentdb.AppendToStreamOptions{}, probe)
		require.Error(t, lastErr, "raw append succeeded while the server was down")
		if connectionClosed(lastErr) {
			poisoned = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.True(t, poisoned,
		"the store's current client never reached ErrorCodeConnectionClosed while down (last error: %v)", lastErr)

	require.NoError(t, ctr.Start(ctx))

	// Re-prove health via the independent store, on a DIFFERENT key so the
	// target stream's revision does not advance before the pinned publish.
	probeId := we.AggregateId{Type: "reconnect", Key: "pin-health-probe"}
	healthy = false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		lastErr = freshStore.Publish(ctx, probeId, we.Options(), TestEvent{Value: "pin-probe"})
		if lastErr == nil {
			healthy = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !healthy {
		t.Fatalf("restarted server never accepted a publish from the probe store within 30s; last error: %v", lastErr)
	}

	// Authoritative pre-pin state of the target stream, read through the
	// independent store so the poisoned client stays untouched.
	prePin, err := freshStore.Load(ctx, id)
	require.NoError(t, err)
	require.NotEmpty(t, prePin.Events)

	// One call, exact expectation, poisoned client: ErrorCodeConnectionClosed
	// pre-dispatch -> rebuild -> duplicate-safe retry -> success.
	err = store.Publish(ctx, id, we.Options(we.WithExpectedRevision(prePin.Revision)), TestEvent{Value: "pin-exact-retry"})
	require.NoError(t, err,
		"exact-expectation publish on a poisoned client must succeed within a single call via the rebuild-and-retry branch")

	// Exactly one append landed: the first attempt was refused pre-dispatch,
	// the retry appended once — no silent duplicate.
	postPin, err := store.Load(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, len(prePin.Events)+1, len(postPin.Events),
		"the pinned publish must append exactly one event")
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
