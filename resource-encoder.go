package we

import (
	"encoding/json"
	"net/http"
)

type EntitySerializer[T any] func(entity *Entity[T]) (map[string]any, error)

func StateSerializer[T any](entity *Entity[T]) (map[string]any, error) {
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

type ResourceEncoder[T any] struct {
	Serializer EntitySerializer[T]
}

func (encoder ResourceEncoder[T]) Encode(w http.ResponseWriter, r *http.Request, e *Entity[T]) error {
	serialize := encoder.Serializer
	if serialize == nil {
		serialize = StateSerializer[T]
	}

	resource, err := serialize(e)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	resource["$id"] = e.Aggregate.Encode()
	resource["$type"] = e.Type
	resource["$revision"] = e.Revision

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resource)

	return nil
}
