package we

import (
	"encoding/json"
	"fmt"
)

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

func MarshalToData(event any) (Data, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return Data{}, err
	}

	return Data{
		Encoding: "application/json",
		Data:     data,
	}, nil
}

func UnmarshalFromData(data Data, value any) error {
	if data.Encoding != "application/json" {
		return InvalidEncoding("application/json", data.Encoding)
	}
	return json.Unmarshal(data.Data, value)
}
