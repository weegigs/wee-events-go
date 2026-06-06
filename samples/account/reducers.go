package account

import es "github.com/weegigs/wee-events-go/we"

// opened is the initializer for the genesis event: it constructs the account
// from nothing rather than mutating existing state.
var opened es.InitializerFunction[Account, Opened] = func(event *Opened) (*Account, error) {
	return &Account{Owner: event.Owner}, nil
}

var deposited es.ReducerFunction[Account, Deposited] = func(state *Account, event *Deposited) error {
	state.Balance += event.Amount
	return nil
}

var withdrawn es.ReducerFunction[Account, Withdrawn] = func(state *Account, event *Withdrawn) error {
	state.Balance -= event.Amount
	return nil
}

func Initializers() es.Initializers[Account] {
	return es.Initializers[Account]{
		es.EventTypeOf(Opened{}): opened,
	}
}

func Reducers() es.Reducers[Account] {
	return es.Reducers[Account]{
		es.EventTypeOf(Deposited{}): deposited,
		es.EventTypeOf(Withdrawn{}): withdrawn,
	}
}
