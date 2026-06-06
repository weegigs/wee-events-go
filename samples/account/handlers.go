package account

import (
	"context"
	"errors"

	es "github.com/weegigs/wee-events-go/we"
)

var (
	ErrAlreadyOpen       = errors.New("account already open")
	ErrNotOpen           = errors.New("account not open")
	ErrInsufficientFunds = errors.New("insufficient funds")
)

// open is the creation command. A nil state means the genesis event has not
// run yet, so the account does not exist and may be opened. Every handler
// publishes with the loaded revision as the expected revision, so concurrent
// commands on the same account surface as es.RevisionConflict rather than
// silently overwriting one another.
var open es.CommandHandlerFunction[Account, Open] = func(ctx context.Context, cmd Open, state es.Entity[Account], publish es.EventPublisher) error {
	if state.State != nil {
		return ErrAlreadyOpen
	}
	return publish(ctx, state.Aggregate, es.Options(es.WithExpectedRevision(state.Revision)), Opened(cmd))
}

var deposit es.CommandHandlerFunction[Account, Deposit] = func(ctx context.Context, cmd Deposit, state es.Entity[Account], publish es.EventPublisher) error {
	if state.State == nil {
		return ErrNotOpen
	}
	return publish(ctx, state.Aggregate, es.Options(es.WithExpectedRevision(state.Revision)), Deposited(cmd))
}

var withdraw es.CommandHandlerFunction[Account, Withdraw] = func(ctx context.Context, cmd Withdraw, state es.Entity[Account], publish es.EventPublisher) error {
	if state.State == nil {
		return ErrNotOpen
	}
	if cmd.Amount > state.State.Balance {
		return ErrInsufficientFunds
	}
	return publish(ctx, state.Aggregate, es.Options(es.WithExpectedRevision(state.Revision)), Withdrawn(cmd))
}

func CommandHandlers() es.CommandHandlers[Account] {
	return es.CommandHandlers[Account]{
		es.CommandNameOf(Open{}):     open,
		es.CommandNameOf(Deposit{}):  deposit,
		es.CommandNameOf(Withdraw{}): withdraw,
	}
}
