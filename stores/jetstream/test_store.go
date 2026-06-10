package jetstream

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weegigs/wee-events-go/we"
)

func NewTestStore(ctx context.Context, options ...EventStoreOption) (*EventStore, func(), error) {
	ctr, err := testcontainers.Run(
		ctx,
		"nats:alpine",
		testcontainers.WithExposedPorts("4222/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("4222/tcp")),
		testcontainers.WithCmd("--jetstream"),
	)
	if err != nil {
		return nil, nil, err
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, nil, err
	}

	port, err := ctr.MappedPort(ctx, "4222")
	if err != nil {
		return nil, nil, err
	}

	url := fmt.Sprintf("nats://%s:%s", host, port.Port())
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, nil, err
	}

	// Test scaffolding names the recommended interop encoding (ENCODING-S2.R4).
	store, err := NewEventStore(ctx, "test", nc, we.MakeJSONEncoder(), options...)
	if err != nil {
		return nil, nil, err
	}

	return store, func() {
		if err := ctr.Terminate(ctx); err != nil {
			panic(err)
		}
	}, nil
}
