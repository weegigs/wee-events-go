package sqlite

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func tursoConfigFromEnv(t *testing.T) TursoConfig {
	t.Helper()
	cfg := TursoConfig{
		Org:        os.Getenv("TURSO_ORG"),
		Group:      os.Getenv("TURSO_GROUP"),
		Prefix:     os.Getenv("TURSO_DB_PREFIX"),
		APIToken:   os.Getenv("TURSO_API_TOKEN"),
		GroupToken: os.Getenv("TURSO_GROUP_TOKEN"),
	}
	if cfg.Org == "" || cfg.Group == "" || cfg.Prefix == "" || cfg.APIToken == "" || cfg.GroupToken == "" {
		t.Skip("TURSO_* environment not set; skipping live Turso test")
	}
	return cfg
}

// TestTursoLiveRoundTrip provisions a real prefixed database, round-trips and
// enumerates events, then deletes the database it created. Skipped unless the
// full TURSO_* set is present (available in the mise environment).
func TestTursoLiveRoundTrip(t *testing.T) {
	cfg := tursoConfigFromEnv(t)
	ctx := context.Background()

	store, err := NewStore(ctx, we.MakeJSONEncoder(), Turso(cfg, ByType()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	id := we.AggregateId{Type: "live-test", Key: "1"}
	t.Cleanup(func() {
		client := newHTTPTursoClient(cfg.APIToken)
		_ = client.DeleteDatabase(context.Background(), cfg.Org, cfg.Prefix+"-live-test")
	})

	require.NoError(t, store.Publish(ctx, id, we.Options(), testEvent{Value: "live"}))

	agg, err := store.Load(ctx, id)
	require.NoError(t, err)
	require.Len(t, agg.Events, 1)

	ids, err := store.EnumerateAggregatesByType(ctx, "live-test")
	require.NoError(t, err)
	assert.Contains(t, ids, id)
}
