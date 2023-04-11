package jetstream

import (
	"github.com/oklog/ulid/v2"
	"github.com/weegigs/wee-events-go/we"
)

type IDGenerator interface {
	Create() we.EventID
}

func WithIdGenerator(generator IDGenerator) EventStoreOption {
	return func(store *EventStore) {
		store.id = generator
	}
}

func NewDefaultIdGenerator(clock Clock) IDGenerator {
	return &DefaultIdGenerator{
		clock: clock,
	}
}

type DefaultIdGenerator struct {
	clock Clock
}

func (g *DefaultIdGenerator) Create() we.EventID {
	v := ulid.MustNew(ulid.Timestamp(g.clock.Now()), ulid.DefaultEntropy()).String()
	return we.EventID(v)
}
