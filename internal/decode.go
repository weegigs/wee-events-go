package decode

import (
	"errors"
	"fmt"
	"reflect"
)

func extractDestValue(v interface{}) (reflect.Value, error) {
	if v == nil {
		return reflect.Value{}, errors.New("argument must be a non-nil pointer")
	}

	rv := reflect.ValueOf(v)

	if rv.Kind() != reflect.Ptr {
		return reflect.Value{}, fmt.Errorf("argument must be a pointer, not %s", rv.Kind())
	}

	if rv.IsNil() {
		return reflect.Value{}, errors.New("argument must be a non-nil pointer")
	}

	return rv.Elem(), nil
}
