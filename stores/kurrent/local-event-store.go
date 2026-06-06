package kurrent

import (
	"context"
	"fmt"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
)

// Creates a new KurrentEventStore instance configured to connect to a local, insecure, KurrentDB instance.
func NewLocalKurrentStore(ctx context.Context, options ...EventStoreOption) (*KurrentEventStore, error) {

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
