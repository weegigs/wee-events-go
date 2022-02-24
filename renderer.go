package we

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
)

type Renderer[T any] struct {
	Initializers map[EventType]Initializer[T]
	Reducers     map[EventType]Reducer[T]
}

func (r *Renderer[T]) Render(ctx context.Context, aggregate Aggregate) (Entity[T], error) {
	var state *T
	var err error

	for _, event := range aggregate.Events {
		eventName := EventTypeOf(event)

		if state == nil {
			initializer := r.Initializers[event.EventType]
			if nil == initializer {
				continue
			}

			_, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("initialize %s", eventName))
			defer span.End()
			state, err = initializer.Initialize(&event)
			if err != nil {
				return Entity[T]{}, errors.Wrap(err, fmt.Sprintf("failed to initialize state with %s", eventName))
			}
		} else {
			reducer := r.Reducers[event.EventType]
			if nil == reducer {
				continue
			}

			_, span := otel.Tracer(tracerName).Start(ctx, fmt.Sprintf("process %s", eventName))
			defer span.End()
			if err := reducer.Reduce(state, &event); err != nil {
				return Entity[T]{}, errors.Wrap(err, fmt.Sprintf("failed to process update with %s", eventName))
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
