package we

type ServiceDescriptor[T any] struct {
	Handlers     map[CommandName]func() CommandHandler[T]
	Initializers map[EventType]func() Initializer[T]
	Reducers     map[EventType]func() Reducer[T]
}
