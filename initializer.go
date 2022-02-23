package we

import (
	"errors"

	"github.com/goccy/go-json"
)

type Initializer[T any] interface {
	Initialize(evt *RecordedEvent) (*T, error)
}

type InitializerFunction[T any, E any] func(evt *E) (*T, error)

func (f InitializerFunction[T, E]) Initialize(evt *RecordedEvent) (*T, error) {
	var event E

	if evt.Data.Encoding != "application/json" {
		return nil, errors.New("unsupported encoding")
	}

	if err := json.Unmarshal(evt.Data.Data, &event); err != nil {
		return nil, err
	}

	return f(&event)
}
