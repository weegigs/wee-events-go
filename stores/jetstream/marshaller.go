package jetstream

import "encoding/json"

func WithMarshaller(marshaller Marshaller) EventStoreOption {
	return func(store *EventStore) {
		store.marshaller = marshaller
	}
}

type Marshaller interface {
	Unmarshal(data []byte, v any) error
	Marshal(v any) ([]byte, error)
}

type JSONMarshaller struct{}

func (J JSONMarshaller) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (J JSONMarshaller) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
