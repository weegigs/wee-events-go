package we

import (
	"encoding/json"
	"fmt"
	"net/http"
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

// Encode writes the entity as a JSON resource. The resource is fully
// serialized before any byte reaches the wire: a serialization failure returns
// the error without touching the ResponseWriter, leaving the response — status
// code and body — to the caller. Internal error text is never written to the
// client.
func (encoder ResourceEncoder[T]) Encode(w http.ResponseWriter, r *http.Request, e Entity[T]) error {
	serialize := encoder.Serializer
	if serialize == nil {
		serialize = StateSerializer[T]
	}

	resource, err := serialize(e)
	if err != nil {
		return fmt.Errorf("failed to serialize resource: %w", err)
	}

	resource["$id"] = e.Aggregate.Encode()
	resource["$type"] = e.Type
	resource["$revision"] = e.Revision

	body, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("failed to encode resource: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("failed to write resource: %w", err)
	}

	return nil
}
