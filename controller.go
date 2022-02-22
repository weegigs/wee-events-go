package we

import (
	"context"
)

type EntityService[T any] interface {
	Load(ctx context.Context, id AggregateId) (*Entity[T], error)
	Execute(ctx context.Context, id AggregateId, command Command) (*Entity[T], error)
}
