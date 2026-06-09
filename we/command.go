package we

import (
	"context"
)

type CommandName string
type Command any

type RemoteCommand struct {
	CommandName CommandName `json:"command"`
	Payload     Data        `json:"payload"`
}

type NamedCommand interface {
	CommandName() CommandName
}

func CommandNameOf(command Command) CommandName {
	var name CommandName
	switch cmd := command.(type) {
	case RemoteCommand:
		name = cmd.CommandName
	default:
		if named, ok := command.(NamedCommand); ok {
			return named.CommandName()
		}

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

	// KAO - decode the payload using the registered decoder for the declared
	// encoding (CBOR-S4.R1); an unsupported encoding is rejected with a typed
	// unknown-encoding error (CBOR-S4.R2). A decode failure — unsupported
	// encoding or malformed bytes — is an inbound CLIENT error, distinct from a
	// store/infrastructure failure and from a domain Rejection, so it is wrapped
	// as a typed *DecodeError the boundary can classify as a 4xx / terminal
	// error (REJECT-S3.R1, ADR-0005). The underlying typed cause stays
	// recoverable through the wrap.
	if err := UnmarshalFromData(cmd.Payload, &command); err != nil {
		return CommandDecodeFailed(cmd.CommandName, err)
	}

	// KAO - a handler that returns a Rejection refuses the command as a domain
	// outcome; it is propagated unchanged (not wrapped) so a boundary recovers
	// it via errors.As and records no event for it (REJECT-S1.R2, REJECT-S1.R4).
	return f(ctx, command, state, publish)
}
