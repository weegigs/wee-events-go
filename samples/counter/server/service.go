package main

import (
	"github.com/google/wire"
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

var service = wire.NewSet(
	counter.PseudoRandomizer,
	NewCounterService,
)

var Live = wire.NewSet(service, ds.Live)

var Local = wire.NewSet(service, ds.Local)
