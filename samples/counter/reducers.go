package counter

import "github.com/weegigs/wee-events-go/we"

// reducers
var incremented we.ReducerFunction[Counter, Incremented] = func(counter *Counter, incremented *Incremented) error {
	counter.Current = counter.Current + incremented.Amount
	return nil
}

var decremented we.ReducerFunction[Counter, Decremented] = func(counter *Counter, decremented *Decremented) error {
	counter.Current = counter.Current - decremented.Amount
	return nil
}

var randomized we.ReducerFunction[Counter, Randomized] = func(counter *Counter, randomized *Randomized) error {
	counter.Current = randomized.Value
	return nil
}

type CounterReducers = we.Reducers[Counter]

func Reducers() CounterReducers {
	return CounterReducers{
		we.EventTypeOf(Incremented{}): incremented,
		we.EventTypeOf(Decremented{}): decremented,
		we.EventTypeOf(Randomized{}):  randomized,
	}
}
