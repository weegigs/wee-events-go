package we

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
)

type Reducers[T any] map[EventType]Reducer[T]

type Renderer[T any] struct {
	Reducers Reducers[T]
}

func (r *Renderer[T]) Render(ctx context.Context, aggregate Aggregate) (Entity[T], error) {
	var state T

	_, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("render %s", NameOf(state)))
	defer span.End()

	for _, event := range aggregate.Events {
		eventType := event.EventType

		reducer := r.Reducers[event.EventType]
		if nil == reducer {
			continue
		}

		if err := reducer.Reduce(&state, &event); err != nil {
			return Entity[T]{}, fmt.Errorf("failed to process update with %s: %w", eventType, err)
		}
	}

	return Entity[T]{
		Aggregate: aggregate.Id,
		Revision:  aggregate.Revision,
		Type:      EntityTypeOf(state),
		State:     &state,
	}, nil
}
