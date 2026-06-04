package esdbs

import (
	"context"
	"fmt"
	"time"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// kurrentDBImage pins the KurrentDB server (the EventStoreDB successor) to a
// concrete LTS tag. The store talks only gRPC append/read, so the node needs
// nothing beyond insecure mode — projections and AtomPub are left off. The
// HTTP/gRPC port and cluster size default to 2113 and 1 respectively.
const kurrentDBImage = "kurrentplatform/kurrentdb:26.0.3"

func NewESDBTestStore(ctx context.Context, options ...EventStoreOption) (*ESDBEventStore, func(), error) {
	ctr, err := testcontainers.Run(
		ctx,
		kurrentDBImage,
		testcontainers.WithEnv(map[string]string{
			"KURRENTDB_INSECURE": "true",
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

	connection := fmt.Sprintf("kurrentdb://admin:changeit@%s:%s?tls=false", host, port.Port())

	settings, err := kurrentdb.ParseConnectionString(connection)
	if err != nil {
		return nil, nil, err
	}

	client, err := kurrentdb.NewClient(settings)
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
