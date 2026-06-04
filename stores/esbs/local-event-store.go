package esdbs

import (
	"context"
	"fmt"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
)

// Creates a new ESDBEventStore instance configured to connect to a local, insecure, esdb instance.
func NewLocalESDBStore(ctx context.Context, options ...EventStoreOption) (*ESDBEventStore, error) {

	connection := fmt.Sprintf("kurrentdb://admin:changeit@%s:%s?tls=false", "localhost", "2113")

	settings, err := kurrentdb.ParseConnectionString(connection)
	if err != nil {
		return nil, err
	}

	client, err := kurrentdb.NewClient(settings)
	if err != nil {
		return nil, err
	}

	store := NewEventStore(client, options...)

	return store, nil
}
