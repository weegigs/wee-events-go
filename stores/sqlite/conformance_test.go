package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/weegigs/wee-events-go/we"
)

// The validation suite must pass for every local strategy: the partitioning
// layer must not change single-store semantics. The global strategy treats its
// root as the single database file, so it receives a file path; the named
// strategies treat their root as a directory of per-shard files.
func TestValidationSuiteLocalStrategies(t *testing.T) {
	cases := []struct {
		name       string
		strat      func() LocalStrategy
		rootIsFile bool
	}{
		{"global", func() LocalStrategy { return Global() }, true},
		{"by_type", func() LocalStrategy { return ByType() }, false},
		{"by_aggregate", func() LocalStrategy { return ByAggregate() }, false},
		{"hashed", func() LocalStrategy { return Hashed(8) }, false},
		{"partition_by", func() LocalStrategy { return PartitionBy(func(id we.AggregateId) string { return id.Type }) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			if tc.rootIsFile {
				root = filepath.Join(root, "events.db")
			}
			store, err := NewStore(ctx, we.MakeJSONEncoder(), Local(root, tc.strat()))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			we.NewEventStoreValidationSuite(ctx, store).Run(t)
		})
	}
}

// Shared-backing: two stores over one local file layout must observe each
// other's commits and enforce cross-instance revision conflicts per shard.
func TestSharedBackingLocalByType(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := NewStore(ctx, we.MakeJSONEncoder(), Local(dir, ByType()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewStore(ctx, we.MakeJSONEncoder(), Local(dir, ByType()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	we.NewSharedBackingSuite(ctx, first, second).Run(t)
}
