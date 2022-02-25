package counter

import (
	"context"

	"github.com/weegigs/wee-events-go/we"
)

// commands
var increment we.CommandHandlerFunction[Counter, Increment] = func(ctx context.Context, cmd Increment, state we.Entity[Counter], publish we.EventPublisher) error {
	return publish(ctx, state.Aggregate, we.Options(), Incremented{Amount: cmd.Amount})
}

var decrement we.CommandHandlerFunction[Counter, Decrement] = func(ctx context.Context, cmd Decrement, state we.Entity[Counter], publish we.EventPublisher) error {
	return publish(ctx, state.Aggregate, we.Options(), Decremented{Amount: cmd.Amount})
}

func randomize(randomizer Randomizer) we.CommandHandler[Counter] {
	var handler we.CommandHandlerFunction[Counter, Randomize] = func(ctx context.Context, cmd Randomize, state we.Entity[Counter], publish we.EventPublisher) error {
		amount := randomizer()

		return publish(ctx, state.Aggregate, we.Options(), Randomized{Value: amount})
	}

	return handler
}

type CounterCommandHandlers = we.CommandHandlers[Counter]

func CommandHandlers(randomizer Randomizer) CounterCommandHandlers {
	return CounterCommandHandlers{
		we.CommandNameOf(Increment{}): increment,
		we.CommandNameOf(Decrement{}): decrement,
		we.CommandNameOf(Randomize{}): randomize(randomizer),
	}
}
