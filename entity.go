package we

type EntityType string

type Entity[T any] struct {
	Aggregate AggregateId
	Revision  Revision
	Type      EntityType
	State     *T
}

type EntityID struct {
	Aggregate AggregateId
	Type      EntityType
}

func (e *Entity[T]) Initialized() bool {
	return e.State != nil
}
