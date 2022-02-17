package es

import (
  "context"
  "errors"
  "fmt"
)

type Controller[T any] interface {
  Load(ctx context.Context, id AggregateId) (*Entity[T], error)
  Execute(ctx context.Context, id AggregateId, command Command) (*Entity[T], error)
}

func NewController[T any](
  entityType EntityType,
  handlers map[CommandType]CommandHandler[T],
  initializers map[EventType]Initializer[T],
  reducers map[EventType]Reducer[T]) func(store EventStore) Controller[T] {

  return func(store EventStore) Controller[T] {
    return &controller[T]{
      store:        store,
      entityType:   entityType,
      handlers:     handlers,
      initializers: initializers,
      reducers:     reducers,
    }
  }
}

type controller[T any] struct {
  store        EventStore
  entityType   EntityType
  initializers map[EventType]Initializer[T]
  reducers     map[EventType]Reducer[T]
  handlers     map[CommandType]CommandHandler[T]
}

func (c *controller[T]) Load(ctx context.Context, id AggregateId) (*Entity[T], error) {
  aggregate, err := c.store.Load(ctx, id)
  if err != nil {
    return nil, err
  }

  var state *T
  for _, event := range aggregate.Events {
    if state == nil {
      initializer := c.initializers[event.EventType]
      if nil == initializer {
        continue
      }

      state, err = initializer(&event)
      if err != nil {
        return nil, err
      }
    } else {
      reducer := c.reducers[event.EventType]
      if nil == reducer {
        continue
      }

      if err = reducer(state, &event); err != nil {
        return nil, err
      }
    }
  }

  return &Entity[T]{
    Aggregate: id,
    Revision:  aggregate.Revision,
    Type:      c.entityType,
    State:     state,
  }, nil
}

func (c *controller[T]) Execute(ctx context.Context, id AggregateId, command Command) (*Entity[T], error) {
  s, err := c.Load(ctx, id)
  if err != nil {
    return nil, err
  }

  h := c.handlers[command.Type()]
  if h == nil {
    return nil, errors.New(fmt.Sprintf("no handler found for command %s", command.Type()))
  }

  err = h(ctx, command, s, c.store.Publish)
  if err != nil {
    return nil, err
  }

  return c.Load(ctx, id)
}
