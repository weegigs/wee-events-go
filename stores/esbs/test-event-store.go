package esdbs

import (
	"context"
	"fmt"

	"github.com/EventStore/EventStore-Client-Go/esdb"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func NewESDBTestStore(ctx context.Context, options ...EventStoreOption) (*ESDBEventStore, func(), error) {

	db, err := testcontainers.GenericContainer(
		ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: "eventstore/eventstore:latest",
				Env: map[string]string{
					"EVENTSTORE_CLUSTER_SIZE":               "1",
					"EVENTSTORE_RUN_PROJECTIONS":            "All",
					"EVENTSTORE_START_STANDARD_PROJECTIONS": "true",
					"EVENTSTORE_EXT_TCP_PORT":               "1113",
					"EVENTSTORE_HTTP_PORT":                  "2113",
					"EVENTSTORE_INSECURE":                   "true",
					"EVENTSTORE_ENABLE_EXTERNAL_TCP":        "true",
					"EVENTSTORE_ENABLE_ATOM_PUB_OVER_HTTP":  "true",
				},
				ExposedPorts: []string{"2113/tcp", "1113/tcp"},
				WaitingFor:   wait.ForListeningPort("2113"),
			},
			Started: true,
		},
	)
	if err != nil {
		return nil, nil, err
	}

	host, err := db.Host(ctx)
	if err != nil {
		return nil, nil, err
	}

	port, err := db.MappedPort(ctx, "2113")
	if err != nil {
		return nil, nil, err
	}

	connection := fmt.Sprintf("esdb://admin:changeit@%s:%s?tls=false", host, port.Port())

	settings, err := esdb.ParseConnectionString(connection)
	if err != nil {
		return nil, nil, err
	}

	client, err := esdb.NewClient(settings)

	store := NewEventStore(client, options...)

	return store, func() {
		if err := db.Terminate(ctx); err != nil {
			panic(err)
		}
	}, nil
}
