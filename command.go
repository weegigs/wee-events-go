package es

import (
  "context"
  "errors"
  "fmt"
)

type CommandType string

type Command interface {
  Type() CommandType
}

type CommandHandler[T any] func(ctx context.Context, cmd Command, state *Entity[T], publish EventPublisher) error

type compositeHandlerBuilder[T any] struct {
  handlers map[CommandType]CommandHandler[T]
}

func CompositeHandler[T any](handlers ...struct {
  CommandType
  CommandHandler[T]
}) *compositeHandlerBuilder[T] {
  return &compositeHandlerBuilder[T]{handlers: make(map[CommandType]CommandHandler[T])}
}

func (b *compositeHandlerBuilder[T]) AddHandler(command CommandType, handler CommandHandler[T]) *compositeHandlerBuilder[T] {
  if b.handlers[command] != nil {
    panic("multiple handlers registered for command")
  }

  b.handlers[command] = handler

  return b
}

func (b *compositeHandlerBuilder[T]) Build() CommandHandler[T] {
  var handler CommandHandler[T] = func(ctx context.Context, command Command, state *Entity[T], publish EventPublisher) error {
    h := b.handlers[command.Type()]
    if h == nil {
      return errors.New(fmt.Sprintf("no handler found for command %s", command.Type()))
    }

    return h(ctx, command, state, publish)
  }

  return handler
}
