package sqlite

import (
	"context"
	"database/sql"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// localCatalog maps partitions to local SQLite files. The Global strategy uses
// root as a single .db file; named strategies use root as a directory holding
// one "b32-<BASE32_NOPAD(name)>.db" per partition. The base32 encoding (rather
// than the raw name) keeps the on-disk layout byte-compatible with wee-events.rs.
type localCatalog struct {
	root     string
	strategy PartitionStrategy
	single   bool
}

var b32enc = base32.StdEncoding.WithPadding(base32.NoPadding)

func newLocalCatalog(root string, strategy LocalStrategy) *localCatalog {
	_, single := strategy.(*global)
	return &localCatalog{root: root, strategy: strategy, single: single}
}

func (c *localCatalog) pathFor(p Partition) string {
	if c.single {
		return c.root
	}
	return filepath.Join(c.root, "b32-"+b32enc.EncodeToString([]byte(c.strategy.PartitionName(p)))+".db")
}

func (c *localCatalog) EnsureTarget(_ context.Context, p Partition) (Target, error) {
	path := c.pathFor(p)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Target{}, fmt.Errorf("sqlite: failed to create partition directory: %w", err)
	}
	return Target{dsn: "file:" + path}, nil
}

func (c *localCatalog) ExistingTarget(_ context.Context, p Partition) (Target, bool, error) {
	path := c.pathFor(p)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return Target{}, false, nil
		}
		return Target{}, false, fmt.Errorf("sqlite: failed to stat partition file: %w", err)
	}
	return Target{dsn: "file:" + path}, true, nil
}

func (c *localCatalog) Partitions(_ context.Context) ([]Partition, error) {
	if c.single {
		return []Partition{DefaultPartition()}, nil
	}

	entries, err := os.ReadDir(c.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sqlite: failed to list partition directory: %w", err)
	}

	var partitions []Partition
	for _, entry := range entries {
		name := entry.Name()
		encoded, ok := strings.CutPrefix(name, "b32-")
		if !ok || !strings.HasSuffix(encoded, ".db") {
			continue
		}
		decoded, err := b32enc.DecodeString(strings.TrimSuffix(encoded, ".db"))
		if err != nil {
			continue
		}
		partition, err := c.strategy.PartitionFromName(string(decoded))
		if err != nil {
			continue
		}
		partitions = append(partitions, partition)
	}
	return partitions, nil
}

func (c *localCatalog) PrepareShard(ctx context.Context, p Partition, db *sql.DB) error {
	if c.single {
		return nil
	}
	return ensurePartitionName(ctx, db, c.strategy.PartitionName(p))
}
