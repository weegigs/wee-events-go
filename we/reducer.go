package we

type Reducer[T any] interface {
	Reduce(state *T, evt *RecordedEvent) error
}

type ReducerFunction[T any, E any] func(state *T, evt *E) error

func (f ReducerFunction[T, E]) et() E {
	var instance E
	return instance
}

func (f ReducerFunction[T, E]) Reduce(state *T, evt *RecordedEvent) error {
	var event E
	if err := UnmarshalFromData(evt.Data, &event); err != nil {
		return err
	}

	return f(state, &event)
}
