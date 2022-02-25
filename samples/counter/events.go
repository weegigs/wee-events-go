package counter

import es "github.com/weegigs/wee-events-go/we"

type Incremented struct {
	Amount int `json:"amount"`
}

type Decremented struct {
	Amount int `json:"amount"`
}

var RandomizedEvent = es.EventType("counter:randomized")

type Randomized struct {
	Value int `json:"value"`
}

func (Randomized) EventType() es.EventType {
	return RandomizedEvent
}
