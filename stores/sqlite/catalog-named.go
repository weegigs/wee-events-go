package sqlite

import (
	"context"
	"database/sql"
	"sort"
)

// PartitionName is the wire name of a partition passed to a provisioner. The
// default partition maps to a backend-specific default; named partitions carry
// the strategy name.
type PartitionName struct {
	name      string
	isDefault bool
}

func (n PartitionName) String() string  { return n.name }
func (n PartitionName) IsDefault() bool { return n.isDefault }

func partitionName(strategy PartitionStrategy, p Partition) PartitionName {
	if p.IsDefault() {
		return PartitionName{isDefault: true}
	}
	return PartitionName{name: strategy.PartitionName(p)}
}

// NamedTarget pairs a provisioned database's wire name with its target.
type NamedTarget struct {
	Name   string
	Target Target
}

// Provisioner creates and lists per-partition databases for the named-target
// backends (sqld namespaces, Turso platform databases).
type Provisioner interface {
	EnsureTarget(ctx context.Context, name PartitionName) (Target, error)
	ExistingTarget(ctx context.Context, name PartitionName) (Target, bool, error)
	NamedTargets(ctx context.Context) ([]NamedTarget, error)
}

// namedTargetCatalog maps each partition to its own provisioned database.
type namedTargetCatalog struct {
	strategy    PartitionStrategy
	provisioner Provisioner
}

func newNamedTargetCatalog(strategy NamingStrategy, provisioner Provisioner) *namedTargetCatalog {
	return &namedTargetCatalog{strategy: strategy, provisioner: provisioner}
}

func (c *namedTargetCatalog) EnsureTarget(ctx context.Context, p Partition) (Target, error) {
	return c.provisioner.EnsureTarget(ctx, partitionName(c.strategy, p))
}

func (c *namedTargetCatalog) ExistingTarget(ctx context.Context, p Partition) (Target, bool, error) {
	return c.provisioner.ExistingTarget(ctx, partitionName(c.strategy, p))
}

// Partitions discovers partitions by listing provisioned databases and reading
// each one's recorded logical name, falling back to strategy discovery from the
// database's wire name for databases that predate the metadata table or cannot
// be opened. Mirrors the Rust three-step.
func (c *namedTargetCatalog) Partitions(ctx context.Context) ([]Partition, error) {
	named, err := c.provisioner.NamedTargets(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]Partition{}
	for _, nt := range named {
		logical := c.logicalName(ctx, nt)
		if logical == "" {
			// Fall back to deriving the logical name from the wire name.
			logical = nt.Name
		}
		partition, err := c.strategy.PartitionFromName(logical)
		if err != nil {
			continue
		}
		seen[partition.Name()] = partition
	}

	partitions := make([]Partition, 0, len(seen))
	for _, p := range seen {
		partitions = append(partitions, p)
	}
	sort.Slice(partitions, func(i, j int) bool { return partitions[i].Name() < partitions[j].Name() })
	return partitions, nil
}

// logicalName reads a database's recorded partition name, opening it directly.
// A target that cannot be opened or has no recorded name yields "" so the
// caller can fall back to the wire name.
func (c *namedTargetCatalog) logicalName(ctx context.Context, nt NamedTarget) string {
	db, err := sql.Open(driverName, nt.Target.dsn)
	if err != nil {
		return ""
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	name, err := readPartitionName(ctx, db)
	if err != nil {
		return ""
	}
	return name
}

func (c *namedTargetCatalog) PrepareShard(ctx context.Context, p Partition, db *sql.DB) error {
	return ensurePartitionName(ctx, db, c.strategy.PartitionName(p))
}
