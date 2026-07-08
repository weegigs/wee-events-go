package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func TestEnumerateAcrossTypePartitions(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), ByType()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ids := []we.AggregateId{
		{Type: "order", Key: "1"},
		{Type: "order", Key: "2"},
		{Type: "user", Key: "kevin"},
	}
	for _, id := range ids {
		require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}))
	}

	got, err := store.EnumerateAggregates(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, got)
}

func TestEnumerateByTypeNarrows(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), ByType()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	for _, id := range []we.AggregateId{{Type: "order", Key: "1"}, {Type: "user", Key: "kevin"}} {
		require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}))
	}

	got, err := store.EnumerateAggregatesByType(ctx, "order")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "order", got[0].Type)
	assert.Equal(t, "1", got[0].Key)
}

func TestEnumerateByTypeNarrowsScanAllStrategy(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), Hashed(4)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	for _, id := range []we.AggregateId{
		{Type: "order", Key: "1"},
		{Type: "order", Key: "2"},
		{Type: "user", Key: "kevin"},
	} {
		require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}))
	}

	got, err := store.EnumerateAggregatesByType(ctx, "order")
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, id := range got {
		assert.Equal(t, "order", id.Type)
	}
}

func TestEnumerateByAggregateUsesDirect(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), ByAggregate()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	id := we.AggregateId{Type: "order", Key: "1"}
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}))

	got, err := store.EnumerateAggregates(ctx)
	require.NoError(t, err)
	assert.Equal(t, []we.AggregateId{id}, got)
}

func TestEnumerateAggregatesReopensStoppedShard(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), ByType()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	id := we.AggregateId{Type: "order", Key: "1"}
	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}))

	partition := store.strategy.PartitionFor(id)
	sh, err := store.ensureShard(ctx, partition)
	require.NoError(t, err)
	sh.stop()

	got, err := store.EnumerateAggregates(ctx)
	require.NoError(t, err)
	assert.Equal(t, []we.AggregateId{id}, got)
}
