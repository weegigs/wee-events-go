package counter

import (
	"context"

	we "github.com/weegigs/wee-events-go"
)

// commands
func increment() we.CommandHandler[Counter] {
	var handler we.CommandHandlerFunction[Counter, Increment] = func(ctx context.Context, cmd Increment, state we.Entity[Counter], publish we.EventPublisher) error {
		_, err := publish(ctx, state.Aggregate, we.Options(), Incremented{Amount: cmd.Amount})
		return err
	}

	return handler
}

func decrement() we.CommandHandler[Counter] {
	var handler we.CommandHandlerFunction[Counter, Decrement] = func(ctx context.Context, cmd Decrement, state we.Entity[Counter], publish we.EventPublisher) error {
		_, err := publish(ctx, state.Aggregate, we.Options(), Decremented{Amount: cmd.Amount})
		return err
	}

	return handler
}

func randomize(randomizer Randomizer) we.CommandHandler[Counter] {
	var handler we.CommandHandlerFunction[Counter, Randomize] = func(ctx context.Context, cmd Randomize, state we.Entity[Counter], publish we.EventPublisher) error {
		amount := randomizer()

		_, err := publish(ctx, state.Aggregate, we.Options(), Randomized{Value: amount})
		return err
	}

	return handler
}
