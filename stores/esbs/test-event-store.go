package esdbs

import (
	"context"
	"fmt"
	"time"

	"github.com/EventStore/EventStore-Client-Go/esdb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// eventStoreImage pins a known-working EventStoreDB image.
//
// KAO - `:latest` now resolves to a 24.x+/KurrentDB build that removed the
// legacy external TCP protocol and rejects EVENTSTORE_EXT_TCP_PORT /
// EVENTSTORE_ENABLE_EXTERNAL_TCP (the container exits with code 1, FTL
// "The option ExtTcpPort is not a known option"). 23.10.0-jammy is the last
// LTS published under the eventstore/eventstore name and speaks the gRPC
// protocol expected by EventStore-Client-Go v1.0.2.
const eventStoreImage = "eventstore/eventstore:23.10.0-jammy"

func NewESDBTestStore(ctx context.Context, options ...EventStoreOption) (*ESDBEventStore, func(), error) {
	ctr, err := testcontainers.Run(
		ctx,
		eventStoreImage,
		testcontainers.WithEnv(map[string]string{
			"EVENTSTORE_CLUSTER_SIZE":               "1",
			"EVENTSTORE_RUN_PROJECTIONS":            "All",
			"EVENTSTORE_START_STANDARD_PROJECTIONS": "true",
			"EVENTSTORE_HTTP_PORT":                  "2113",
			"EVENTSTORE_INSECURE":                   "true",
			"EVENTSTORE_ENABLE_ATOM_PUB_OVER_HTTP":  "true",
		}),
		testcontainers.WithExposedPorts("2113/tcp"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("2113/tcp").WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		return nil, nil, err
	}

	port, err := ctr.MappedPort(ctx, "2113")
	if err != nil {
		return nil, nil, err
	}

	connection := fmt.Sprintf("esdb://admin:changeit@%s:%s?tls=false", host, port.Port())

	settings, err := esdb.ParseConnectionString(connection)
	if err != nil {
		return nil, nil, err
	}

	client, err := esdb.NewClient(settings)
	if err != nil {
		return nil, nil, err
	}

	store := NewEventStore(client, options...)

	return store, func() {
		if err := ctr.Terminate(ctx); err != nil {
			panic(err)
		}
	}, nil
}
