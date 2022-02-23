package we

import (
	"fmt"
)

type EventMarshaller interface {
	Marshal(event any) (Data, error)
	Unmarshal(data Data, value any) error
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
