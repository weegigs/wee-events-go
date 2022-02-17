package es

type Initializer[T any] func(evt *DecodedEvent) (*T, error)
type Reducer[T any] func(state *T, evt *DecodedEvent) error
