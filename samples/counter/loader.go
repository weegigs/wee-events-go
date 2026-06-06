package counter

import "github.com/weegigs/wee-events-go/we"

func Loader(store we.EventStore) *we.EntityLoader[Counter] {
	// A counter's zero value (0) is a valid starting state, so it is seeded
	// with an init function rather than a genesis event.
	renderer := we.Renderer[Counter]{
		Init:     func(we.AggregateId) *Counter { return &Counter{} },
		Reducers: Reducers(),
	}
	loader := we.EntityLoader[Counter]{Loader: store.Load, Renderer: &renderer}

	return &loader
}
