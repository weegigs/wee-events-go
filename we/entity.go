package we

type EntityType string

func (et EntityType) String() string {
  return string(et)
}

type EntityTyped interface {
  EntityType() EntityType
}

func EntityTypeOf(state any) EntityType {
  if named, ok := state.(EntityTyped); ok == true {
    return named.EntityType()
  }

  return EntityType(NameOf(state))
}

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
  return e.Revision != InitialRevision
}
