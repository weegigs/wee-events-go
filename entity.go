package es

type EntityType string

type Entity[T any] struct {
  Aggregate AggregateId
  Revision  Revision
  Type      EntityType
  State     *T
}

func (e *Entity[T]) Initialised() bool {
  return e.State != nil
}
