package we

import (
	"context"
	"errors"
	"fmt"
)

func CreateController[T any](
	events EventStore,
	config ServiceDescriptor[T],
) *Controller[T] {
	handlers := make(map[CommandName]CommandHandler[T], len(config.Handlers))
	for name, handler := range config.Handlers {
		handlers[name] = handler()
	}

	initializers := make(map[EventType]Initializer[T], len(config.Initializers))
	for name, initializer := range config.Initializers {
		initializers[name] = initializer()
	}

	reducers := make(map[EventType]Reducer[T], len(config.Reducers))
	for name, reducer := range config.Reducers {
		reducers[name] = reducer()
	}

	return &Controller[T]{
		events:       events,
		handlers:     handlers,
		initializers: initializers,
		reducers:     reducers,
	}
}

type Controller[T any] struct {
	events       EventStore
	handlers     map[CommandName]CommandHandler[T]
	initializers map[EventType]Initializer[T]
	reducers     map[EventType]Reducer[T]
}

func (controller *Controller[T]) Load(ctx context.Context, id AggregateId) (*Entity[T], error) {
	aggregate, err := controller.events.Load(ctx, id)
	if err != nil {
		return nil, err
	}

	var state *T
	for _, event := range aggregate.Events {
		if state == nil {
			initializer := controller.initializers[event.EventType]
			if nil == initializer {
				continue
			}

			state, err = initializer.Initialize(&event)
			if err != nil {
				return nil, err
			}
		} else {
			reducer := controller.reducers[event.EventType]
			if nil == reducer {
				continue
			}

			if err = reducer.Reduce(state, &event); err != nil {
				return nil, err
			}
		}
	}

	return &Entity[T]{
		Aggregate: aggregate.Id,
		Revision:  aggregate.Revision,
		Type:      EntityType(NameOf(state)),
		State:     state,
	}, nil
}

func (controller *Controller[T]) Execute(ctx context.Context, id AggregateId, command Command) (*Entity[T], error) {
	s, err := controller.Load(ctx, id)
	if err != nil {
		return nil, err
	}

	revision := s.Revision
	cmd := CommandNameOf(command)

	var publish EventPublisher = func(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) (Revision, error) {
		revision, err = controller.events.Publish(ctx, aggregateId, options, events...)
		return revision, err
	}

	h := controller.handlers[cmd]
	if h == nil {
		return nil, errors.New(fmt.Sprintf("no handler found for command %s", cmd))
	}

	if err = h.HandleCommand(ctx, command, s, publish); err != nil {
		return nil, err
	}

	if revision == s.Revision {
		return s, nil
	} else {
		return controller.Load(ctx, id)
	}
}
