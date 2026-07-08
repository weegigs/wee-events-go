package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	sh := newShard(db, we.MakeJSONEncoder(), defaultBusyTimeout, true, nil)
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

func TestShardStopLetsInFlightRequestFinishBeforeClosingDB(t *testing.T) {
	ctx := context.Background()
	sh := newTestShard(t)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		_, err := sh.dispatch(ctx, func(ctx context.Context, db *sql.DB) (any, error) {
			close(started)
			<-release
			var one int
			return nil, db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
		})
		done <- err
	}()

	<-started
	sh.stop()
	close(release)

	require.NoError(t, <-done)
	_, err := sh.dispatch(ctx, func(context.Context, *sql.DB) (any, error) {
		return nil, nil
	})
	require.ErrorIs(t, err, errShardClosed)
	assert.False(t, errors.Is(err, sql.ErrConnDone))
}

func TestShardAwaitClosedBlocksUntilDatabaseClosed(t *testing.T) {
	ctx := context.Background()
	sh := newTestShard(t)
	started := make(chan struct{})
	release := make(chan struct{})
	inflight := make(chan error, 1)

	go func() {
		_, err := sh.dispatch(ctx, func(ctx context.Context, db *sql.DB) (any, error) {
			close(started)
			<-release
			var one int
			return nil, db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
		})
		inflight <- err
	}()

	<-started
	sh.stop()

	waited := make(chan struct{})
	go func() {
		sh.awaitClosed()
		close(waited)
	}()

	select {
	case <-waited:
		t.Fatal("awaitClosed returned while a request was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-inflight)

	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("awaitClosed did not return after the owner goroutine exited")
	}

	// The owner goroutine has exited, so the database must actually be closed.
	err := sh.db.Ping()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed")
}

func TestOperationGateWithLimitOneSerializesConcurrentOperations(t *testing.T) {
	gate := newOperationGate(1)
	start := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)

	for range 2 {
		go func() {
			defer wg.Done()
			<-start
			_, err := gate.runValue(context.Background(), func() (any, error) {
				now := active.Add(1)
				for {
					old := peak.Load()
					if now <= old || peak.CompareAndSwap(old, now) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				active.Add(-1)
				return nil, nil
			})
			require.NoError(t, err)
		}()
	}

	close(start)
	wg.Wait()
	assert.Equal(t, int32(1), peak.Load())
}

func TestOperationGateAllowsBoundedParallelism(t *testing.T) {
	gate := newOperationGate(2)
	start := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	var wg sync.WaitGroup
	wg.Add(4)

	for range 4 {
		go func() {
			defer wg.Done()
			<-start
			_, err := gate.runValue(context.Background(), func() (any, error) {
				now := active.Add(1)
				for {
					old := peak.Load()
					if now <= old || peak.CompareAndSwap(old, now) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				active.Add(-1)
				return nil, nil
			})
			require.NoError(t, err)
		}()
	}

	close(start)
	wg.Wait()
	assert.Equal(t, int32(2), peak.Load())
}

func TestOperationGateRespectsContextCancellation(t *testing.T) {
	gate := newOperationGate(1)
	release := make(chan struct{})
	acquired := make(chan struct{})

	go func() {
		_, _ = gate.runValue(context.Background(), func() (any, error) {
			close(acquired)
			<-release
			return nil, nil
		})
	}()
	<-acquired

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := gate.runValue(ctx, func() (any, error) {
		return nil, nil
	})

	close(release)
	require.ErrorIs(t, err, context.Canceled)
}
