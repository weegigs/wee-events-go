package counter

import we "github.com/weegigs/wee-events-go"

// initializers
func incrementInitializer() we.Initializer[Counter] {
	var initializer we.InitializerFunction[Counter, Incremented] = func(incremented *Incremented) (*Counter, error) {
		return &Counter{Current: incremented.Amount}, nil
	}

	return initializer
}

func decrementInitializer() we.Initializer[Counter] {
	var initializer we.InitializerFunction[Counter, Decremented] = func(evt *Decremented) (*Counter, error) {
		return &Counter{Current: evt.Amount}, nil
	}

	return initializer
}

// reducers
func incremented() we.Reducer[Counter] {
	var reducer we.ReducerFunction[Counter, Incremented] = func(counter *Counter, incremented *Incremented) error {
		counter.Current = counter.Current + incremented.Amount
		return nil
	}

	return reducer
}

func decremented() we.Reducer[Counter] {
	var reducer we.ReducerFunction[Counter, Decremented] = func(counter *Counter, decremented *Decremented) error {
		counter.Current = counter.Current - decremented.Amount
		return nil
	}

	return reducer
}

func randomized() we.Reducer[Counter] {
	var reducer we.ReducerFunction[Counter, Randomized] = func(counter *Counter, randomized *Randomized) error {
		counter.Current = randomized.Value
		return nil
	}

	return reducer
}
