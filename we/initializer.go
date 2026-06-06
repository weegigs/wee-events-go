package we

type Initializer[T any] interface {
	Initialize(evt *RecordedEvent) (*T, error)
}

type Initializers[T any] map[EventType]Initializer[T]

type InitializerFunction[T any, E any] func(evt *E) (*T, error)

func (f InitializerFunction[T, E]) Initialize(evt *RecordedEvent) (*T, error) {
	var event E
	if err := UnmarshalFromData(evt.Data, &event); err != nil {
		return nil, err
	}

	return f(&event)
}
