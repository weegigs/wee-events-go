package we

import (
  "context"

  "go.opentelemetry.io/otel"
)

type EntityLoader[T any] struct {
  Loader   EventLoader
  Renderer *Renderer[T]
}

func (s *EntityLoader[T]) Load(ctx context.Context, id AggregateId) (Entity[T], error) {
  ctx, span := otel.Tracer(tracerName).Start(ctx, "load entity")
  defer span.End()

  aggregate, err := s.Loader(ctx, id)
  if err != nil {
    return Entity[T]{}, err
  }

  return s.Renderer.Render(ctx, aggregate)
}
