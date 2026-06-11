package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func newTestShard(t *testing.T) *shard {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open(driverName, ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrate(ctx, db))
	// The in-memory shard uses the embedded SQLite engine (not Hrana), so it runs
	// the local-file write path including busy_timeout.
	sh := newShard(db, we.MakeJSONEncoder(), defaultBusyTimeout, true)
	t.Cleanup(sh.stop)
	return sh
}

func TestShardPublishThenLoad(t *testing.T) {
	ctx := context.Background()
	sh := newTestShard(t)
	id := we.AggregateId{Type: "order", Key: "1"}

	err := sh.publish(ctx, id, we.Options(), testEvent{Value: "a"})
	require.NoError(t, err)

	agg, err := sh.load(ctx, id)
	require.NoError(t, err)
	require.Len(t, agg.Events, 1)
	assert.Equal(t, we.EventTypeOf(testEvent{}), agg.Events[0].EventType)
}

func TestShardSerializesConcurrentPublishes(t *testing.T) {
	ctx := context.Background()
	sh := newTestShard(t)
	id := we.AggregateId{Type: "order", Key: "1"}

	const n = 32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- sh.publish(ctx, id, we.Options(), testEvent{Value: "x"}) }()
	}
	for i := 0; i < n; i++ {
		require.NoError(t, <-errs)
	}

	agg, err := sh.load(ctx, id)
	require.NoError(t, err)
	assert.Len(t, agg.Events, n)
}

func TestShardRespectsContextCancellation(t *testing.T) {
	sh := newTestShard(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := sh.load(ctx, we.AggregateId{Type: "order", Key: "1"})
	require.ErrorIs(t, err, context.Canceled)
}
