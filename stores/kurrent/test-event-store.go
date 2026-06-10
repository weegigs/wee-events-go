package kurrent

import (
	"context"
	"fmt"
	"time"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/weegigs/wee-events-go/we"
)

// kurrentDBImage pins the KurrentDB server (the EventStoreDB successor) to a
// concrete LTS tag. The store talks only gRPC append/read, so the node needs
// nothing beyond insecure mode — projections and AtomPub are left off. The
// HTTP/gRPC port and cluster size default to 2113 and 1 respectively.
const kurrentDBImage = "kurrentplatform/kurrentdb:26.0.3"

// NewKurrentTestStore starts a disposable KurrentDB container and returns a
// store over it. The encoder is the store's explicit write encoding
// (ENCODING-S2.R1).
func NewKurrentTestStore(ctx context.Context, encoder we.Encoder, options ...EventStoreOption) (*KurrentEventStore, func(), error) {
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
		// Run returns the container alongside the error when create/start/wait
		// fails after the container exists (e.g. a wait-strategy timeout), so a
		// non-nil ctr must be terminated here or the failed startup leaks a
		// running container.
		if ctr != nil {
			if terr := ctr.Terminate(ctx); terr != nil {
				panic(terr)
			}
		}
		return nil, nil, err
	}

	// Every error path after the container starts must release it: a failed
	// construction returning early would otherwise leak the running container.
	terminate := func() {
		if err := ctr.Terminate(ctx); err != nil {
			panic(err)
		}
	}

	host, err := ctr.Host(ctx)
	if err != nil {
		terminate()
		return nil, nil, err
	}

	port, err := ctr.MappedPort(ctx, "2113")
	if err != nil {
		terminate()
		return nil, nil, err
	}

	connection := fmt.Sprintf("kurrentdb://admin:changeit@%s:%s?tls=false", host, port.Port())

	settings, err := kurrentdb.ParseConnectionString(connection)
	if err != nil {
		terminate()
		return nil, nil, err
	}

	client, err := kurrentdb.NewClient(settings)
	if err != nil {
		terminate()
		return nil, nil, err
	}

	store, err := NewEventStore(client, encoder, options...)
	if err != nil {
		terminate()
		return nil, nil, err
	}

	return store, terminate, nil
}
