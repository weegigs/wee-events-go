package we

import (
  "reflect"
  "strings"

  "github.com/iancoleman/strcase"
)

type Named interface {
  TypeName() string
}

func NameOf(value any) string {
  if typed, ok := value.(Named); ok == true {
    return typed.TypeName()
  }

  split := strings.Split(reflect.TypeOf(value).String(), ".")
  segments := make([]string, len(split))
  for i, segment := range split {
    s := strings.TrimLeft(segment, "*")
    segments[i] = strcase.ToKebab(s)
  }

  namespace := segments[0]
  event := strings.Join(segments[1:], "-")

  return namespace + ":" + event
}
