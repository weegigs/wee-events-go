package jetstream

import "time"

type Clock interface {
	Now() time.Time
}

func WithClockGenerator(clock Clock) EventStoreOption {
	return func(store *EventStore) {
		store.clock = clock
	}
}

type defaultClock struct {
}

func (defaultClock) Now() time.Time {
	return time.Now()
}
