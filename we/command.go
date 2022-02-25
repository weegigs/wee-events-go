package we

import (
	"context"
	"errors"

	"github.com/goccy/go-json"
)

type CommandName string
type Command any

type RemoteCommand struct {
	CommandName CommandName `json:"command"`
	Payload     Data        `json:"payload"`
}

func CommandNameOf(command Command) CommandName {
	var name CommandName
	switch cmd := command.(type) {
	case RemoteCommand:
		name = cmd.CommandName
	default:
		name = CommandName(NameOf(command))
	}

	return name
}

type CommandHandler[T any] interface {
	HandleCommand(ctx context.Context, cmd Command, state Entity[T], publish EventPublisher) error
	HandleRemoteCommand(ctx context.Context, cmd RemoteCommand, state Entity[T], publish EventPublisher) error
}

type CommandHandlerFunction[T any, C any] func(ctx context.Context, cmd C, state Entity[T], publish EventPublisher) error

func (f CommandHandlerFunction[T, C]) HandleCommand(ctx context.Context, cmd Command, state Entity[T], publish EventPublisher) error {
	command, ok := cmd.(C)
	if !ok {
		return UnexpectedCommand(cmd)
	}

	return f(ctx, command, state, publish)
}

func (f CommandHandlerFunction[T, C]) HandleRemoteCommand(ctx context.Context, cmd RemoteCommand, state Entity[T], publish EventPublisher) error {
	var command C

	if cmd.Payload.Encoding != "application/json" {
		return errors.New("unsupported encoding")
	}

	if err := json.UnmarshalContext(ctx, cmd.Payload.Data, &command); err != nil {
		return err
	}

	return f(ctx, command, state, publish)
}
