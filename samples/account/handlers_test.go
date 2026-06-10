package account

// SURFACE-S1.R1 / R2 — domain guards are we.Rejection with exact codes and
// real context, so both connectors classify them as domain refusals.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	es "github.com/weegigs/wee-events-go/we"
)

// noPublish builds an es.EventPublisher bound to the subtest's t, so a
// publish from a refused command fails the subtest that owns it rather than
// the parent.
func noPublish(t *testing.T) es.EventPublisher {
	return func(context.Context, es.AggregateId, es.PublishOptions, ...es.DomainEvent) error {
		t.Error("a refused command must not publish")
		return nil
	}
}

func TestGuardsReturnTypedRejections(t *testing.T) {
	ctx := context.Background()

	t.Run("open on an open account", func(t *testing.T) {
		state := es.Entity[Account]{State: &Account{Owner: "kevin", Balance: 10}}
		err := open(ctx, Open{Owner: "again"}, state, noPublish(t))
		var rejection es.Rejection
		require.True(t, errors.As(err, &rejection))
		assert.Equal(t, "account.already-open", rejection.Code)
	})

	t.Run("withdraw beyond balance carries real context", func(t *testing.T) {
		state := es.Entity[Account]{State: &Account{Owner: "kevin", Balance: 10}}
		err := withdraw(ctx, Withdraw{Amount: 25}, state, noPublish(t))
		var rejection es.Rejection
		require.True(t, errors.As(err, &rejection))
		assert.Equal(t, "account.insufficient-funds", rejection.Code)
		assert.JSONEq(t, `{"balance":10,"requested":25}`, string(rejection.Context))
	})

	t.Run("deposit on a missing account", func(t *testing.T) {
		err := deposit(ctx, Deposit{Amount: 5}, es.Entity[Account]{}, noPublish(t))
		var rejection es.Rejection
		require.True(t, errors.As(err, &rejection))
		assert.Equal(t, "account.not-open", rejection.Code)
	})
}
