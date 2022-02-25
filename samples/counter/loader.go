package counter

import "github.com/weegigs/wee-events-go/we"

func Loader(store we.EventStore) *we.EntityLoader[Counter] {
	renderer := we.Renderer[Counter]{Initializers: Initializers(), Reducers: Reducers()}
	loader := we.EntityLoader[Counter]{Loader: store.Load, Renderer: &renderer}

	return &loader
}
