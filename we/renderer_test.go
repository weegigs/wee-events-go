package we

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// init-function mode fixture: a tally whose zero value (Count 0) is a valid
// starting state, so it is seeded from an Init function rather than a genesis
// event.
type tally struct {
	Count int `json:"count"`
}

type bumped struct {
	By int `json:"by"`
}

// genesis-event mode fixture: an account that cannot exist until an Opened
// event supplies its Owner, so its zero value is invalid.
type acct struct {
	Owner   string `json:"owner"`
	Balance int    `json:"balance"`
}

type opened struct {
	Owner string `json:"owner"`
}

type credited struct {
	Amount int `json:"amount"`
}

func recorded(t *testing.T, evt DomainEvent) RecordedEvent {
	t.Helper()
	data, err := MarshalToData(MakeJSONEncoder(), evt)
	require.NoError(t, err)
	return RecordedEvent{
		EventType: EventTypeOf(evt),
		Data:      data,
	}
}

const nonInitialRevision = Revision("00000000000000000000000001")

var bump ReducerFunction[tally, bumped] = func(state *tally, event *bumped) error {
	state.Count += event.By
	return nil
}

var openAccount InitializerFunction[acct, opened] = func(event *opened) (*acct, error) {
	return &acct{Owner: event.Owner}, nil
}

var credit ReducerFunction[acct, credited] = func(state *acct, event *credited) error {
	state.Balance += event.Amount
	return nil
}

func tallyRenderer() *Renderer[tally] {
	return &Renderer[tally]{
		Init: func(AggregateId) *tally { return &tally{} },
		Reducers: Reducers[tally]{
			EventTypeOf(bumped{}): bump,
		},
	}
}

func accountRenderer() *Renderer[acct] {
	return &Renderer[acct]{
		Initializers: Initializers[acct]{
			EventTypeOf(opened{}): openAccount,
		},
		Reducers: Reducers[acct]{
			EventTypeOf(credited{}): credit,
		},
	}
}

func TestRenderer(t *testing.T) {
	ctx := context.Background()
	id := AggregateId{Type: "account", Key: "123"}

	t.Run("init-function mode seeds state when no events exist", func(t *testing.T) {
		entity, err := tallyRenderer().Render(ctx, Aggregate{Id: id, Revision: InitialRevision})
		require.NoError(t, err)
		require.NotNil(t, entity.State)
		assert.Equal(t, 0, entity.State.Count)
		assert.False(t, entity.Initialized(), "an aggregate with no events is not initialized")
	})

	t.Run("init-function mode folds reducers onto the seeded state", func(t *testing.T) {
		entity, err := tallyRenderer().Render(ctx, Aggregate{
			Id:       id,
			Revision: nonInitialRevision,
			Events:   []RecordedEvent{recorded(t, bumped{By: 3}), recorded(t, bumped{By: 4})},
		})
		require.NoError(t, err)
		require.NotNil(t, entity.State)
		assert.Equal(t, 7, entity.State.Count)
		assert.True(t, entity.Initialized())
	})

	t.Run("genesis mode reports not-found when no genesis event exists", func(t *testing.T) {
		entity, err := accountRenderer().Render(ctx, Aggregate{Id: id, Revision: InitialRevision})
		require.NoError(t, err)
		assert.Nil(t, entity.State, "no genesis event means the entity does not exist")
		assert.False(t, entity.Initialized())
	})

	t.Run("genesis mode constructs state from the genesis event then folds reducers", func(t *testing.T) {
		entity, err := accountRenderer().Render(ctx, Aggregate{
			Id:       id,
			Revision: nonInitialRevision,
			Events: []RecordedEvent{
				recorded(t, opened{Owner: "alice"}),
				recorded(t, credited{Amount: 10}),
				recorded(t, credited{Amount: 5}),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, entity.State)
		assert.Equal(t, "alice", entity.State.Owner)
		assert.Equal(t, 15, entity.State.Balance)
		assert.True(t, entity.Initialized())
	})

	t.Run("genesis mode skips reducer events that precede the genesis event", func(t *testing.T) {
		entity, err := accountRenderer().Render(ctx, Aggregate{
			Id:       id,
			Revision: nonInitialRevision,
			Events: []RecordedEvent{
				recorded(t, credited{Amount: 99}), // skipped: state does not exist yet
				recorded(t, opened{Owner: "bob"}),
				recorded(t, credited{Amount: 5}),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, entity.State)
		assert.Equal(t, "bob", entity.State.Owner)
		assert.Equal(t, 5, entity.State.Balance, "the pre-genesis credit must not apply")
	})

	t.Run("genesis mode stays not-found when events exist but none are a genesis event", func(t *testing.T) {
		entity, err := accountRenderer().Render(ctx, Aggregate{
			Id:       id,
			Revision: nonInitialRevision,
			Events:   []RecordedEvent{recorded(t, credited{Amount: 5})},
		})
		require.NoError(t, err)
		assert.Nil(t, entity.State)
		assert.False(t, entity.Initialized(), "a non-nil revision alone must not mark an uninitialised entity as found")
	})

	t.Run("reducer errors propagate", func(t *testing.T) {
		boom := errors.New("reducer boom")
		var failing ReducerFunction[tally, bumped] = func(*tally, *bumped) error { return boom }
		renderer := &Renderer[tally]{
			Init:     func(AggregateId) *tally { return &tally{} },
			Reducers: Reducers[tally]{EventTypeOf(bumped{}): failing},
		}
		_, err := renderer.Render(ctx, Aggregate{
			Id:       id,
			Revision: nonInitialRevision,
			Events:   []RecordedEvent{recorded(t, bumped{By: 1})},
		})
		assert.ErrorIs(t, err, boom)
	})

	t.Run("initializer errors propagate", func(t *testing.T) {
		boom := errors.New("initializer boom")
		var failing InitializerFunction[acct, opened] = func(*opened) (*acct, error) { return nil, boom }
		renderer := &Renderer[acct]{
			Initializers: Initializers[acct]{EventTypeOf(opened{}): failing},
		}
		_, err := renderer.Render(ctx, Aggregate{
			Id:       id,
			Revision: nonInitialRevision,
			Events:   []RecordedEvent{recorded(t, opened{Owner: "alice"})},
		})
		assert.ErrorIs(t, err, boom)
	})
}
