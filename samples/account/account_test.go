package account_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/samples/account"
	es "github.com/weegigs/wee-events-go/we"
)

// memoryStore is a minimal in-process es.EventStore so the sample can be
// exercised end to end without Docker. It honours ExpectedRevision so the
// optimistic-concurrency path in the handlers is real, not decorative.
type memoryStore struct {
	mu        sync.Mutex
	streams   map[es.EncodedAggregateId][]es.RecordedEvent
	revisions *es.RevisionGenerator
	encoder   es.Encoder
}

// newMemoryStore mirrors the real store constructors: the caller names the
// write encoding explicitly and the store holds it for the life of the
// instance (ENCODING-S2.R1, ENCODING-S2.R4).
func newMemoryStore(encoder es.Encoder) *memoryStore {
	return &memoryStore{
		streams:   map[es.EncodedAggregateId][]es.RecordedEvent{},
		revisions: es.NewRevisionGenerator(),
		encoder:   encoder,
	}
}

func (s *memoryStore) revisionOf(events []es.RecordedEvent) es.Revision {
	if len(events) == 0 {
		return es.InitialRevision
	}
	return events[len(events)-1].Revision
}

func (s *memoryStore) Load(_ context.Context, id es.AggregateId) (es.Aggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := s.streams[id.Encode()]
	events := append([]es.RecordedEvent(nil), stored...)
	return es.Aggregate{Id: id, Events: events, Revision: s.revisionOf(stored)}, nil
}

func (s *memoryStore) Publish(_ context.Context, id es.AggregateId, options es.PublishOptions, events ...es.DomainEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := s.streams[id.Encode()]
	if options.ExpectedRevision != "" && options.ExpectedRevision != s.revisionOf(stored) {
		return es.RevisionConflict
	}

	// An empty publish is a no-op, not an error (CONFORMANCE-S3); it
	// short-circuits before override validation, matching the real stores.
	if len(events) == 0 {
		return nil
	}

	// Resolve the publish encoder the way real stores do: an explicit
	// per-publish override wins over the store's constructor encoder
	// (ENCODING-S2.R3, ENCODING-S2.R5).
	encoder, err := options.EncoderFor(s.encoder)
	if err != nil {
		return fmt.Errorf("memory: %w", err)
	}

	now := time.Now()
	for _, evt := range events {
		data, err := es.MarshalToData(encoder, evt)
		if err != nil {
			return err
		}
		revision := s.revisions.NewRevision(now)
		stored = append(stored, es.RecordedEvent{
			AggregateId: id,
			Revision:    revision,
			EventID:     es.EventID(revision.String()),
			EventType:   es.EventTypeOf(evt),
			Timestamp:   es.TimestampFromTime(now),
			Metadata:    options.RecordedEventMetadata,
			Data:        data,
		})
	}
	s.streams[id.Encode()] = stored
	return nil
}

func TestAccount(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore(es.MakeJSONEncoder())
	service := account.Service(store)

	t.Run("an account that was never opened is not found", func(t *testing.T) {
		entity, err := service.Load(ctx, es.AggregateId{Type: "account", Key: "ghost"})
		require.NoError(t, err)
		assert.Nil(t, entity.State, "no genesis event means no state")
		assert.False(t, entity.Initialized())
	})

	t.Run("a command before the account is opened is rejected", func(t *testing.T) {
		_, err := service.Execute(ctx, es.AggregateId{Type: "account", Key: "premature"}, account.Deposit{Amount: 50})
		assert.ErrorIs(t, err, account.ErrNotOpen)
	})

	id := es.AggregateId{Type: "account", Key: "alice"}

	t.Run("opening creates the account from the genesis event", func(t *testing.T) {
		entity, err := service.Execute(ctx, id, account.Open{Owner: "alice"})
		require.NoError(t, err)
		require.True(t, entity.Initialized())
		require.NotNil(t, entity.State)
		assert.Equal(t, "alice", entity.State.Owner)
		assert.Equal(t, 0, entity.State.Balance)
	})

	t.Run("opening an already-open account is rejected", func(t *testing.T) {
		_, err := service.Execute(ctx, id, account.Open{Owner: "mallory"})
		assert.ErrorIs(t, err, account.ErrAlreadyOpen)
	})

	t.Run("deposits and withdrawals evolve the balance", func(t *testing.T) {
		_, err := service.Execute(ctx, id, account.Deposit{Amount: 100})
		require.NoError(t, err)

		entity, err := service.Execute(ctx, id, account.Withdraw{Amount: 30})
		require.NoError(t, err)
		assert.Equal(t, 70, entity.State.Balance)
	})

	t.Run("withdrawing more than the balance is rejected", func(t *testing.T) {
		_, err := service.Execute(ctx, id, account.Withdraw{Amount: 1000})
		assert.ErrorIs(t, err, account.ErrInsufficientFunds)
	})
}
