package we

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
)

type Reducers[T any] map[EventType]Reducer[T]

// Renderer folds an aggregate's events into entity state. State is brought into
// existence one of two ways:
//
//   - Init seeds a default state from the aggregate id. Used when the zero value
//     is a valid starting state (e.g. a counter at 0). The entity always has
//     state, though it remains uninitialised until an event advances it.
//   - Initializers construct state from a genesis event. Used when the zero
//     value is invalid (e.g. an account that needs an owner). Until a genesis
//     event is seen the state is nil and the entity reports as not found.
//
// Reducers evolve state that already exists. Once state exists, events are
// handled by Reducers; while it does not, events are handled by Initializers.
type Renderer[T any] struct {
	Init         func(AggregateId) *T
	Initializers Initializers[T]
	Reducers     Reducers[T]
}

func (r *Renderer[T]) Render(ctx context.Context, aggregate Aggregate) (Entity[T], error) {
	var zero T
	_, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("render %s", NameOf(zero)))
	defer span.End()

	var state *T
	if r.Init != nil {
		state = r.Init(aggregate.Id)
	}

	for _, event := range aggregate.Events {
		eventType := event.EventType

		if state == nil {
			initializer := r.Initializers[eventType]
			if initializer == nil {
				continue
			}

			initialized, err := initializer.Initialize(&event)
			if err != nil {
				return Entity[T]{}, fmt.Errorf("failed to initialize with %s: %w", eventType, err)
			}
			state = initialized
			continue
		}

		reducer := r.Reducers[eventType]
		if reducer == nil {
			continue
		}

		if err := reducer.Reduce(state, &event); err != nil {
			return Entity[T]{}, fmt.Errorf("failed to process update with %s: %w", eventType, err)
		}
	}

	return Entity[T]{
		Aggregate: aggregate.Id,
		Revision:  aggregate.Revision,
		Type:      EntityTypeOf(zero),
		State:     state,
	}, nil
}
