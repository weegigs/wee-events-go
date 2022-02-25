package esdbs

import (
	"context"
	"fmt"

	"github.com/EventStore/EventStore-Client-Go/esdb"
)

// Creates a new ESDBEventStore instance configured to connect to a local, insecure, esdb instance.
func NewLocalESDBStore(ctx context.Context, options ...EventStoreOption) (*ESDBEventStore, error) {

	connection := fmt.Sprintf("esdb://admin:changeit@%s:%s?tls=false", "localhost", "2113")

	settings, err := esdb.ParseConnectionString(connection)
	if err != nil {
		return nil, err
	}

	client, err := esdb.NewClient(settings)
	store := NewEventStore(client, options...)

	return store, nil
}
