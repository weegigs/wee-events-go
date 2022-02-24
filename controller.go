package we

import (
	"context"

	"go.opentelemetry.io/otel"
)

func CreateController[T any](
	events EventStore,
	descriptor ServiceDescriptor[T],
) *Controller[T] {
	handlers := make(map[CommandName]CommandHandler[T], len(descriptor.Handlers))
	for name, handler := range descriptor.Handlers {
		handlers[name] = handler()
	}

	initializers := make(map[EventType]Initializer[T], len(descriptor.Initializers))
	for name, initializer := range descriptor.Initializers {
		initializers[name] = initializer()
	}

	reducers := make(map[EventType]Reducer[T], len(descriptor.Reducers))
	for name, reducer := range descriptor.Reducers {
		reducers[name] = reducer()
	}

	renderer := &Renderer[T]{
		Initializers: initializers,
		Reducers:     reducers,
	}

	dispatcher := &Dispatcher[T]{
		Publish:  events.Publish,
		Handlers: handlers,
	}

	return &Controller[T]{
		load:       events.Load,
		dispatcher: dispatcher,
		renderer:   renderer,
	}
}

type Controller[T any] struct {
	load       EventLoader
	dispatcher *Dispatcher[T]
	renderer   *Renderer[T]
}

const tracerName = "events-controller"

func (controller *Controller[T]) Load(ctx context.Context, id AggregateId) (Entity[T], error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "load entity")
	defer span.End()

	aggregate, err := controller.load(ctx, id)
	if err != nil {
		return Entity[T]{}, err
	}

	return controller.renderer.Render(ctx, aggregate)
}

func (controller *Controller[T]) Execute(ct context.Context, id AggregateId, command Command) (Entity[T], error) {
	ctx, span := otel.Tracer(tracerName).Start(ct, "execute command")
	defer span.End()
	entity, err := controller.Load(ctx, id)
	if err != nil {
		return Entity[T]{}, err
	}

	revision, err := controller.dispatcher.Dispatch(ctx, entity, command)
	if err != nil {
		return Entity[T]{}, err
	}

	if revision == entity.Revision {
		return entity, nil
	} else {
		return controller.Load(ctx, id)
	}
}
