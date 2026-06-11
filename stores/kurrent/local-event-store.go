package kurrent

import (
	"context"
	"fmt"

	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"

	"github.com/weegigs/wee-events-go/we"
)

// Creates a new KurrentEventStore instance configured to connect to a local, insecure, KurrentDB instance.
// The encoder is the store's explicit write encoding (ENCODING-S2.R1).
func NewLocalKurrentStore(ctx context.Context, encoder we.Encoder, options ...EventStoreOption) (*KurrentEventStore, error) {

	connection := fmt.Sprintf("kurrentdb://admin:changeit@%s:%s?tls=false", "localhost", "2113")

	settings, err := kurrentdb.ParseConnectionString(connection)
	if err != nil {
		return nil, err
	}

	return NewEventStore(settings, encoder, options...)
}
