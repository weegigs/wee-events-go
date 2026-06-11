package sqlite

import (
	"context"
	"database/sql"
)

// singleTargetCatalog maps every partition to one provisioned target. It backs
// the in-memory and sqld-default backends, where all aggregates share one
// database. Enumeration over a single target returns no partitions; callers
// fall back to the store's known set.
type singleTargetCatalog struct {
	target Target
}

func newSingleTargetCatalog(target Target) *singleTargetCatalog {
	return &singleTargetCatalog{target: target}
}

func (c *singleTargetCatalog) EnsureTarget(context.Context, Partition) (Target, error) {
	return c.target, nil
}
func (c *singleTargetCatalog) ExistingTarget(context.Context, Partition) (Target, bool, error) {
	return c.target, true, nil
}
func (c *singleTargetCatalog) Partitions(context.Context) ([]Partition, error) {
	return nil, nil
}
func (c *singleTargetCatalog) PrepareShard(context.Context, Partition, *sql.DB) error {
	return nil
}
