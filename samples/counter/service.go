package counter

import (
	we "github.com/weegigs/wee-events-go"
)

type CounterServiceDependencies struct {
	Randomizer Randomizer
}

func CreateCounterDescriptor(dependencies CounterServiceDependencies) we.ServiceDescriptor[Counter] {

	initializers := map[we.EventType]func() we.Initializer[Counter]{
		we.EventTypeOf(Incremented{}): incrementInitializer,
		we.EventTypeOf(Decremented{}): decrementInitializer,
	}

	reducers := map[we.EventType]func() we.Reducer[Counter]{
		we.EventTypeOf(Incremented{}): incremented,
		we.EventTypeOf(Decremented{}): decremented,
	}

	handlers := map[we.CommandName]func() we.CommandHandler[Counter]{
		we.CommandNameOf(Increment{}): increment,
		we.CommandNameOf(Decrement{}): decrement,
	}

	handlers[we.CommandNameOf(Randomize{})] = func() we.CommandHandler[Counter] { return randomize(dependencies.Randomizer) }

	return we.ServiceDescriptor[Counter]{
		Initializers: initializers,
		Reducers:     reducers,
		Handlers:     handlers,
	}
}

func CreateCounterService(randomizer Randomizer, store we.EventStore) we.EntityService[Counter] {
	descriptor := CreateCounterDescriptor(CounterServiceDependencies{Randomizer: randomizer})
	controller := we.CreateController(store, descriptor)

	return controller
}
