package we

import (
	"context"
	"fmt"
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

func (controller *Controller[T]) Load(ctx context.Context, id AggregateId) (Entity[T], error) {
	aggregate, err := controller.events.Load(ctx, id)
	if err != nil {
		return Entity[T]{}, err
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
				return Entity[T]{}, err
			}
		} else {
			reducer := controller.reducers[event.EventType]
			if nil == reducer {
				continue
			}

			if err = reducer.Reduce(state, &event); err != nil {
				return Entity[T]{}, err
			}
		}
	}

	return Entity[T]{
		Aggregate: aggregate.Id,
		Revision:  aggregate.Revision,
		Type:      EntityType(NameOf(state)),
		State:     state,
	}, nil
}

func (controller *Controller[T]) Execute(ctx context.Context, id AggregateId, command Command) (Entity[T], error) {
	entity, err := controller.Load(ctx, id)
	if err != nil {
		return Entity[T]{}, err
	}

	commandName := CommandNameOf(command)
	revision := entity.Revision

	handler := controller.handlers[commandName]
	if handler == nil {
		return Entity[T]{}, CommandNotFound(commandName)
	}

	var publish EventPublisher = func(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) (Revision, error) {
		revision, err = controller.events.Publish(ctx, aggregateId, options, events...)
		return revision, err
	}

	if err = controller.execute(ctx, handler, command, entity, publish); err != nil {
		return Entity[T]{}, err
	}

	if revision == entity.Revision {
		return entity, nil
	} else {
		return controller.Load(ctx, id)
	}
}

func (controller *Controller[T]) Commands() []CommandName {
	var names []CommandName
	for name := range controller.handlers {
		names = append(names, name)
	}

	return names
}

func (controller *Controller[T]) execute(ctx context.Context, handler CommandHandler[T], command Command, state Entity[T], publish EventPublisher) error {
	switch cmd := command.(type) {
	case RemoteCommand:
		return handler.HandleRemoteCommand(ctx, cmd, state, publish)
	default:
		return handler.HandleCommand(ctx, cmd, state, publish)
	}
}

func CommandNotFound(command CommandName) CommandNotFoundError {
	return CommandNotFoundError{Command: command}
}

type CommandNotFoundError struct {
	Command CommandName
}

func (e CommandNotFoundError) Error() string {
	return fmt.Sprintf("unknown command: %s", e.Command)
}
