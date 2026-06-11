package we

import (
	"encoding/json"
	"fmt"

	"github.com/fxamacker/cbor/v2"
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

// resource builds the entity's resource map — the serializer's state fields
// plus the $id/$type/$revision identity metadata. The map is the single
// source of truth for every medium's rendering: Marshal and MarshalCBOR each
// render it natively, never by transcoding another medium's text.
func (encoder ResourceEncoder[T]) resource(e Entity[T]) (map[string]any, error) {
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

	return resource, nil
}

// Marshal serializes the entity to the bytes of its JSON resource
// representation. It is pure: a failure returns before any byte reaches a
// transport, so the caller (typically a connector) alone owns the response
// (SURFACE-S4.R3). Internal error text never reaches the resource bytes.
func (encoder ResourceEncoder[T]) Marshal(e Entity[T]) ([]byte, error) {
	resource, err := encoder.resource(e)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to encode resource: %w", err)
	}
	return body, nil
}

// MarshalCBOR serializes the entity to the bytes of its CBOR resource
// representation — the same resource map Marshal renders, in the CBOR medium
// (ADR-0011 decision 5). It carries Marshal's guarantees: pure, a failure
// returns before any byte reaches a transport (SURFACE-S4.R3), and internal
// error text never reaches the resource bytes.
func (encoder ResourceEncoder[T]) MarshalCBOR(e Entity[T]) ([]byte, error) {
	resource, err := encoder.resource(e)
	if err != nil {
		return nil, err
	}

	body, err := cbor.Marshal(resource)
	if err != nil {
		return nil, fmt.Errorf("failed to encode resource: %w", err)
	}
	return body, nil
}
