package sqlite

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

// TestConcurrentLoadPublishNoMisuse is the regression gate for the go-libsql
// SQLITE_MISUSE defect: many goroutines load and publish across shards with no
// failure. Run under -race in `just test`.
func TestConcurrentLoadPublishNoMisuse(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(t.TempDir(), Hashed(8)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := we.AggregateId{Type: "order", Key: fmt.Sprintf("agg-%d", w)}
			for range 20 {
				if err := store.Publish(ctx, id, we.Options(), testEvent{Value: "x"}); err != nil {
					errs <- err
					return
				}
				if _, err := store.Load(ctx, id); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}
