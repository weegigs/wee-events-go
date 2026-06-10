package we

// EntityEncoder serializes an entity to the bytes of its resource
// representation. The returned bytes must form a JSON document; connectors
// serve them with a JSON content type. The encoder is pure: marshalling
// failures return before any byte reaches a transport, so the caller
// (typically a connector) alone owns the response (SURFACE-S4.R3).
type EntityEncoder[T any] interface {
	Marshal(e Entity[T]) ([]byte, error)
}
