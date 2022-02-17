package counter_example

import (
  "context"

  es "github.com/weegigs/wee-events-go"
)

func increment(ctx context.Context, cmd es.Command, state *es.Entity[Counter], publish es.EventPublisher) error {
  command, ok := cmd.(Increment)
  if !ok {
    return es.UnexpectedCommand(cmd)
  }

  _, err := publish(ctx, state.Aggregate, es.Options(), Incremented{Amount: command.Amount})
  return err
}

func decrement(ctx context.Context, cmd es.Command, state *es.Entity[Counter], publish es.EventPublisher) error {
  command, ok := cmd.(Decrement)
  if !ok {
    return es.UnexpectedCommand(cmd)
  }

  _, err := publish(ctx, state.Aggregate, es.Options(), Decremented{Amount: command.Amount})
  return err
}

func initiallyIncremented(evt *es.DecodedEvent) (*Counter, error) {
  incremented, ok := evt.Payload.(Incremented)
  if !ok {
    return nil, es.UnexpectedEvent(evt)
  }

  return &Counter{current: incremented.Amount}, nil
}

func incremented(counter *Counter, evt *es.DecodedEvent) error {
  incremented, ok := evt.Payload.(Incremented)
  if !ok {
    return es.UnexpectedEvent(evt)
  }

  counter.current = counter.current + incremented.Amount

  return nil
}

func decremented(counter *Counter, evt *es.DecodedEvent) error {
  decremented, ok := evt.Payload.(Decremented)
  if !ok {
    return es.UnexpectedEvent(evt)
  }

  counter.current = counter.current - decremented.Amount

  return nil
}

func CounterController() func(store es.EventStore) es.Controller[Counter] {
  handlers := make(map[es.CommandType]es.CommandHandler[Counter])
  handlers[IncrementCmd] = increment
  handlers[DecrementCmd] = decrement

  initializers := make(map[es.EventType]es.Initializer[Counter])
  initializers[IncrementedEvent] = initiallyIncremented

  reducers := make(map[es.EventType]es.Reducer[Counter])
  reducers[IncrementedEvent] = incremented
  reducers[DecrementedEvent] = decremented

  return es.NewController[Counter]("counter", handlers, initializers, reducers)
}

type Counter struct {
  current int
}

func (state *Counter) Value() int {
  return state.current
}
