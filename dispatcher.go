package we

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
)

type Dispatcher[T any] struct {
	Publish  EventPublisher
	Handlers map[CommandName]CommandHandler[T]
}

func (d *Dispatcher[T]) Dispatch(ctx context.Context, entity Entity[T], command Command) (Revision, error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("dispatch %s", CommandNameOf(command)))
	defer span.End()

	var err error
	revision := entity.Revision

	commandName := CommandNameOf(command)
	handler := d.Handlers[commandName]
	if handler == nil {
		return "", CommandNotFound(commandName)
	}

	var publish EventPublisher = func(ctx context.Context, aggregateId AggregateId, options PublishOptions, events ...DomainEvent) (Revision, error) {
		revision, err = d.Publish(ctx, aggregateId, options, events...)
		return revision, err
	}

	if err = d.execute(ctx, handler, command, entity, publish); err != nil {
		return "", err
	}

	return revision, nil
}

func (d *Dispatcher[T]) execute(ctx context.Context, handler CommandHandler[T], command Command, state Entity[T], publish EventPublisher) error {
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
