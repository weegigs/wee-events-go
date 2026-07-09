package we

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rejectionState is a trivial aggregate state for the propagation tests.
type rejectionState struct {
	Value int
}

type bumpCommand struct{}

func (bumpCommand) TypeName() string { return "test:bump" }

// newRejectionService wires an EntityService over an in-memory store with a
// single "test:bump" handler whose behaviour the caller supplies. It returns
// the service and the backing store so a test can assert what was (or was not)
// recorded.
func newRejectionService(
	t *testing.T,
	handle func(ctx context.Context, cmd bumpCommand, state Entity[rejectionState], publish EventPublisher) error,
) (*entityService[rejectionState], *memoryEventStore) {
	t.Helper()

	store := newMemoryEventStore(MakeJSONEncoder())

	loader := &EntityLoader[rejectionState]{
		Loader: Loader(store),
		Renderer: &Renderer[rejectionState]{
			Init: func(AggregateId) *rejectionState { return &rejectionState{} },
			Reducers: Reducers[rejectionState]{
				"we:bumpEvent": ReducerFunction[rejectionState, bumpEvent](func(s *rejectionState, _ *bumpEvent) error {
					s.Value++
					return nil
				}),
			},
		},
	}

	var handler CommandHandlerFunction[rejectionState, bumpCommand] = handle
	dispatcher := &RoutedDispatcher[rejectionState]{
		Publish:  Publisher(store),
		Handlers: CommandHandlers[rejectionState]{CommandNameOf(bumpCommand{}): handler},
	}

	return NewEntityService(loader, dispatcher), store
}

type bumpEvent struct{}

// REJECT-S1.R3 - a Rejection returned by a handler is preserved unchanged
// through the dispatcher and the entity service so errors.As recovers the
// original code, message, and context.
func rejectionSurvivesDispatcherAndService(t *testing.T) {
	want := MakeRejection("bump.refused", "cannot bump in this state",
		map[string]ErrorField{"value": MakeI64Field(0)})

	service, _ := newRejectionService(t, func(_ context.Context, _ bumpCommand, _ Entity[rejectionState], _ EventPublisher) error {
		return want
	})

	_, err := service.Execute(context.Background(), AggregateId{Type: "counter", Key: "a"}, bumpCommand{})
	require.Error(t, err)

	var recovered Rejection
	require.True(t, errors.As(err, &recovered), "expected to recover a Rejection through the service, got %T", err)
	assert.Equal(t, want.Code, recovered.Code)
	assert.Equal(t, want.Message, recovered.Message)
	assert.Equal(t, want.Fields, recovered.Fields)
}

// REJECT-S1.R4 - a handler that refuses a command records no event to the
// stream for that command (a refusal is a no-op against the stream).
func refusedCommandRecordsNoEvent(t *testing.T) {
	service, store := newRejectionService(t, func(_ context.Context, _ bumpCommand, _ Entity[rejectionState], _ EventPublisher) error {
		return MakeRejection("bump.refused", "no", nil)
	})

	id := AggregateId{Type: "counter", Key: "b"}
	_, err := service.Execute(context.Background(), id, bumpCommand{})
	require.Error(t, err)

	aggregate, loadErr := store.Load(context.Background(), id)
	require.NoError(t, loadErr)
	assert.Empty(t, aggregate.Events, "a refused command must record no event")
}

// REJECT-S3.R3 - an infrastructure error propagated through the dispatcher and
// the entity service is not wrapped in a way that causes errors.As to
// misclassify it as a Rejection.
func infrastructureErrorNotMisclassifiedAsRejection(t *testing.T) {
	boom := errors.New("store is on fire")

	service, _ := newRejectionService(t, func(_ context.Context, _ bumpCommand, _ Entity[rejectionState], _ EventPublisher) error {
		return boom
	})

	_, err := service.Execute(context.Background(), AggregateId{Type: "counter", Key: "c"}, bumpCommand{})
	require.Error(t, err)

	var rejection Rejection
	assert.False(t, errors.As(err, &rejection), "an infrastructure error must not classify as a Rejection through the service")
	assert.ErrorIs(t, err, boom, "the original infrastructure error must be preserved")
}

func TestRejectionPropagation(t *testing.T) {
	t.Run("rejection survives dispatcher and service (REJECT-S1.R3)", rejectionSurvivesDispatcherAndService)
	t.Run("refused command records no event (REJECT-S1.R4)", refusedCommandRecordsNoEvent)
	t.Run("infrastructure error not misclassified (REJECT-S3.R3)", infrastructureErrorNotMisclassifiedAsRejection)
}
