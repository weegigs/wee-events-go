package we

import (
	"net/http"
)

type EntityEncoder[T any] interface {
	Encode(w http.ResponseWriter, r *http.Request, e *Entity[T]) error
}
