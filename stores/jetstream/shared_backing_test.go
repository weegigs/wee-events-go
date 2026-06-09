package jetstream

import (
	"context"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/weegigs/wee-events-go/we"
)

// TestSharedBacking wires the shared-backing conformance suite for the JetStream
// store. Two EventStore instances are constructed over the same NATS connection
// and stream name, so they share one backing (CONFORMANCE-S5.R1 / R4). It is an
// integration test: it requires Docker (a NATS JetStream container) and runs
// under `just test-integration`.
func TestSharedBacking(t *testing.T) {
	ctx := context.Background()

	ctr, err := testcontainers.Run(
		ctx,
		"nats:alpine",
		testcontainers.WithExposedPorts("4222/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("4222/tcp")),
		testcontainers.WithCmd("--jetstream"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ctr.Terminate(ctx); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	}()

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}

	port, err := ctr.MappedPort(ctx, "4222")
	if err != nil {
		t.Fatal(err)
	}

	url := fmt.Sprintf("nats://%s:%s", host, port.Port())
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	// Two stores over the same connection and stream name share one backing; the
	// stream is created idempotently via CreateOrUpdateStream.
	a, err := NewEventStore(ctx, "shared", nc)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewEventStore(ctx, "shared", nc)
	if err != nil {
		t.Fatal(err)
	}

	suite := we.NewSharedBackingSuite(ctx, a, b)
	suite.Run(t)
}
