package we

import (
	"context"

	"go.opentelemetry.io/otel"
)

type EntityService[T any] interface {
	Load(ctx context.Context, id AggregateId) (Entity[T], error)
	Execute(ct context.Context, id AggregateId, command Command) (Entity[T], error)
}

func NewEntityService[T any](loader *EntityLoader[T], dispatcher *RoutedDispatcher[T]) *entityService[T] {
	return &entityService[T]{
		loader:     loader,
		dispatcher: dispatcher,
	}
}

type entityService[T any] struct {
	loader     *EntityLoader[T]
	dispatcher *RoutedDispatcher[T]
}

const tracerName = "events-service"

func (s *entityService[T]) Load(ctx context.Context, id AggregateId) (Entity[T], error) {
	// ctx, span := otel.Tracer(tracerName).Start(ctx, "service load entity")
	// defer span.End()
	return s.loader.Load(ctx, id)
}

func (s *entityService[T]) Execute(ctx context.Context, id AggregateId, command Command) (Entity[T], error) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, "execute command")
	defer span.End()

	entity, err := s.Load(ctx, id)
	if err != nil {
		return Entity[T]{}, err
	}

	published, err := s.dispatcher.Dispatch(ctx, entity, command)

	if err != nil {
		return Entity[T]{}, err
	}

	if published == false {
		return entity, nil
	}

	return s.Load(ctx, id)
}
