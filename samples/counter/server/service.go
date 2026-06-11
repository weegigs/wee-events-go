package main

import (
	"context"

	"github.com/weegigs/wee-events-go/samples/counter"
	"github.com/weegigs/wee-events-go/stores/ds"
	"github.com/weegigs/wee-events-go/we"
)

type CounterService = we.EntityService[counter.Counter]

func NewCounterService(store we.EventStore, randomizer counter.Randomizer) CounterService {
	loader := counter.Loader(store)
	dispatcher := we.RoutedDispatcher[counter.Counter]{Handlers: counter.CommandHandlers(randomizer), Publish: store.Publish}

	return we.NewEntityService(loader, &dispatcher)
}

// local composes the service against a local DynamoDB endpoint. JSON names
// the service's write encoding at the composition root (ENCODING-S2.R4):
// it is the recommended interop choice.
func local(ctx context.Context) (CounterService, error) {
	store, err := ds.LocalDynamoStore(ctx, we.MakeJSONEncoder())
	if err != nil {
		return nil, err
	}

	return NewCounterService(store, counter.PseudoRandomizer()), nil
}
