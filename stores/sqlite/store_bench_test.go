package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/weegigs/wee-events-go/we"
)

// BenchmarkSqliteInMemory measures the shared event-store benchmark suite
// against a private in-memory SQLite database.
func BenchmarkSqliteInMemory(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

// BenchmarkSqliteLocalFile measures the shared event-store benchmark suite
// against a local-file SQLite database in a temporary directory.
func BenchmarkSqliteLocalFile(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(filepath.Join(b.TempDir(), "bench.db"), Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}
