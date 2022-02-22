package we

import (
	"context"
)

type CommandName string
type Command any

func CommandNameOf(cmd Command) CommandName {
	return CommandName(NameOf(cmd))
}

type CommandHandler[T any] interface {
	HandleCommand(ctx context.Context, cmd Command, state *Entity[T], publish EventPublisher) error
}

type CommandHandlerFunction[T any, C any] func(ctx context.Context, cmd *C, state *Entity[T], publish EventPublisher) error

func (f CommandHandlerFunction[T, C]) HandleCommand(ctx context.Context, cmd Command, state *Entity[T], publish EventPublisher) error {
	command, ok := cmd.(C)
	if !ok {
		return UnexpectedCommand(cmd)
	}

	return f(ctx, &command, state, publish)
}
