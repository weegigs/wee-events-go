package we

import (
	"encoding/json"
	"fmt"
)

type EntitySerializer[T any] func(entity Entity[T]) (map[string]any, error)

func StateSerializer[T any](entity Entity[T]) (map[string]any, error) {
	serialized, err := json.Marshal(entity.State)
	if err != nil {
		return nil, err
	}
	resource := make(map[string]any)
	if err = json.Unmarshal(serialized, &resource); err != nil {
		return nil, err
	}

	return resource, nil
}

func NewCustomResourceEncoder[T any](serializer EntitySerializer[T]) *ResourceEncoder[T] {
	return &ResourceEncoder[T]{
		Serializer: serializer,
	}
}

func NewResourceEncoder[T any]() *ResourceEncoder[T] {
	return &ResourceEncoder[T]{}
}

type ResourceEncoder[T any] struct {
	Serializer EntitySerializer[T]
}

// Marshal serializes the entity to the bytes of its JSON resource
// representation. It is pure: a failure returns before any byte reaches a
// transport, so the caller (typically a connector) alone owns the response
// (SURFACE-S4.R3). Internal error text never reaches the resource bytes.
func (encoder ResourceEncoder[T]) Marshal(e Entity[T]) ([]byte, error) {
	serialize := encoder.Serializer
	if serialize == nil {
		serialize = StateSerializer[T]
	}

	resource, err := serialize(e)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize resource: %w", err)
	}

	resource["$id"] = e.Aggregate.Encode()
	resource["$type"] = e.Type
	resource["$revision"] = e.Revision

	body, err := json.Marshal(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to encode resource: %w", err)
	}
	return body, nil
}
