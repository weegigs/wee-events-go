package we

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
)

type Initializers[T any] map[EventType]Initializer[T]
type Reducers[T any] map[EventType]Reducer[T]

type Renderer[T any] struct {
	Initializers Initializers[T]
	Reducers     Reducers[T]
}

func (r *Renderer[T]) Render(ctx context.Context, aggregate Aggregate) (Entity[T], error) {
	var state *T
	var err error

	_, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("render %s", NameOf(state)))
	defer span.End()

	for _, event := range aggregate.Events {
		eventType := event.EventType

		if state == nil {
			initializer := r.Initializers[event.EventType]
			if nil == initializer {
				continue
			}

			// _, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("initialize with %s", eventType))
			// defer span.End()
			state, err = initializer.Initialize(&event)
			if err != nil {
				return Entity[T]{}, errors.Wrap(err, fmt.Sprintf("failed to initialize state with %s", eventType))
			}
		} else {
			reducer := r.Reducers[event.EventType]
			if nil == reducer {
				continue
			}

			// _, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("apply %s", eventType))
			// defer span.End()
			if err := reducer.Reduce(state, &event); err != nil {
				return Entity[T]{}, errors.Wrap(err, fmt.Sprintf("failed to process update with %s", eventType))
			}
		}
	}

	return Entity[T]{
		Aggregate: aggregate.Id,
		Revision:  aggregate.Revision,
		Type:      EntityType(NameOf(state)),
		State:     state,
	}, nil
}
