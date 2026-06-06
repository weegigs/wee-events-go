package account

import es "github.com/weegigs/wee-events-go/we"

func Loader(store es.EventStore) *es.EntityLoader[Account] {
	// An account is brought into existence by the Opened genesis event, so the
	// renderer is configured with Initializers rather than an Init function.
	renderer := es.Renderer[Account]{
		Initializers: Initializers(),
		Reducers:     Reducers(),
	}
	return &es.EntityLoader[Account]{Loader: store.Load, Renderer: &renderer}
}

func Service(store es.EventStore) es.EntityService[Account] {
	dispatcher := es.RoutedDispatcher[Account]{Handlers: CommandHandlers(), Publish: store.Publish}
	return es.NewEntityService(Loader(store), &dispatcher)
}
