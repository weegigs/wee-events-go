package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/weegigs/wee-events-go/we"
)

// benchLocal runs the shared event-store benchmark suite against a local-file
// backend rooted at root with the given strategy.
func benchLocal(b *testing.B, root string, strategy LocalStrategy) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(root, strategy))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

// BenchmarkSqliteInMemory measures the in-memory single-database store.
func BenchmarkSqliteInMemory(b *testing.B) {
	ctx := context.Background()
	store, err := NewStore(ctx, we.MakeJSONEncoder(), InMemory(Global()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	we.NewEventStoreBenchmarkSuite(ctx, store).Run(b)
}

// BenchmarkSqliteLocalGlobal uses a single file as the store root: Global routes
// every aggregate to one database, so its root must be a file path, not the
// directory the named strategies use.
func BenchmarkSqliteLocalGlobal(b *testing.B) {
	benchLocal(b, filepath.Join(b.TempDir(), "events.db"), Global())
}

func BenchmarkSqliteLocalByType(b *testing.B)      { benchLocal(b, b.TempDir(), ByType()) }
func BenchmarkSqliteLocalByAggregate(b *testing.B) { benchLocal(b, b.TempDir(), ByAggregate()) }
func BenchmarkSqliteLocalHashed(b *testing.B)      { benchLocal(b, b.TempDir(), Hashed(8)) }
func BenchmarkSqliteLocalPartitionBy(b *testing.B) {
	benchLocal(b, b.TempDir(), PartitionBy(func(id we.AggregateId) string { return id.Type }))
}
