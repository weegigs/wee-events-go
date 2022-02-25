package we

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
)

type CommandHandlers[T any] map[CommandName]CommandHandler[T]

type Dispatcher[T any] interface {
	Dispatch(ctx context.Context, entity Entity[T], command Command) (bool, error)
}

type CommandDispatcher[T any] struct {
	Publish EventPublisher
	Handler CommandHandler[T]
}

func (c *CommandDispatcher[T]) Dispatch(ctx context.Context, entity Entity[T], command Command) (bool, error) {
	// TODO implement me
	panic("implement me")
}

type RoutedDispatcher[T any] struct {
	Publish  EventPublisher
	Handlers CommandHandlers[T]
}

func (d *RoutedDispatcher[T]) Dispatch(ctx context.Context, entity Entity[T], command Command) (bool, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("dispatch %s", CommandNameOf(command)))
	defer span.End()

	commandName := CommandNameOf(command)
	handler := d.Handlers[commandName]
	if handler == nil {
		return false, CommandNotFound(commandName)
	}

	return execute(ctx, handler, command, entity, d.Publish)
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

func execute[T any](ctx context.Context, handler CommandHandler[T], command Command, state Entity[T], publish EventPublisher) (bool, error) {
	tracking := &trackingPublisher{publish: publish}

	switch cmd := command.(type) {
	case RemoteCommand:
		if err := handler.HandleRemoteCommand(ctx, cmd, state, tracking.Publish); err != nil {
			return tracking.published, err
		}
	default:
		if err := handler.HandleCommand(ctx, cmd, state, tracking.Publish); err != nil {
			return tracking.published, err
		}
	}

	return tracking.published, nil
}

type trackingPublisher struct {
	publish   EventPublisher
	published bool
}

func (p *trackingPublisher) Publish(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) error {
	if err := p.publish(ctx, aggregateId, options, events...); err != nil {
		return err
	}

	p.published = p.published || len(events) > 0

	return nil
}
