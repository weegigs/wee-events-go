package we

import (
	"encoding/json"
	"fmt"
)

type EventEncoder interface {
	Encode(event any) (*Data, error)
	Decode(data *Data, value any) error
}

type InvalidEncodingError struct {
	Expected string
	Actual   string
}

func (e *InvalidEncodingError) Error() string {
	return fmt.Sprintf("expected encoding %s, got %s", e.Expected, e.Actual)
}

func InvalidEncoding(expected string, actual string) error {
	return &InvalidEncodingError{
		Expected: expected,
		Actual:   actual,
	}
}

func NewJsonEventEncoder() *JsonEventEncoder {
	return &JsonEventEncoder{}
}

type JsonEventEncoder struct{}

func (JsonEventEncoder) Encode(event any) (*Data, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}

	return &Data{
		Encoding: "application/json",
		Data:     data,
	}, nil
}

func (JsonEventEncoder) Decode(data *Data, value any) error {
	if data.Encoding != "application/json" {
		return InvalidEncoding("application/json", data.Encoding)
	}
	return json.Unmarshal(data.Data, value)
}
