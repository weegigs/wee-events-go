package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/weegigs/wee-events-go/we"
)

// EnumerateAggregates returns every aggregate id across all known partitions.
// It unions the catalog's discovered partitions with partitions the store has
// touched this session, then harvests ids per partition by read plan.
func (s *Store) EnumerateAggregates(ctx context.Context) ([]we.AggregateId, error) {
	return s.enumerate(ctx, func(p Partition) ReadPlan { return s.strategy.ReadPlan(p) })
}

// EnumerateAggregatesByType returns every aggregate of the given type. Type
// partitions that cannot hold the type are skipped; other partitions scan and
// filter.
func (s *Store) EnumerateAggregatesByType(ctx context.Context, aggregateType string) ([]we.AggregateId, error) {
	return s.enumerate(ctx, func(p Partition) ReadPlan {
		plan := s.strategy.ReadPlan(p)
		switch plan.kind {
		case readScanType:
			if plan.aggregateType != aggregateType {
				return Skip()
			}
			return plan
		case readDirect:
			if plan.id.Type != aggregateType {
				return Skip()
			}
			return plan
		default:
			return plan
		}
	})
}

func (s *Store) enumerate(ctx context.Context, planFor func(Partition) ReadPlan) ([]we.AggregateId, error) {
	partitions, err := s.allKnownPartitions(ctx)
	if err != nil {
		return nil, err
	}

	seen := map[string]we.AggregateId{}
	for _, partition := range partitions {
		plan := planFor(partition)
		if plan.kind == readSkip {
			continue
		}
		if plan.kind == readDirect {
			seen[plan.id.Encode().String()] = plan.id
			continue
		}

		sh, ok, err := s.openExisting(ctx, partition)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		ids, err := sh.scan(ctx, func(ctx context.Context, db *sql.DB) ([]we.AggregateId, error) {
			return scanAggregates(ctx, db, plan)
		})
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id.Encode().String()] = id
		}
	}

	out := make([]we.AggregateId, 0, len(seen))
	for _, id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Encode().String() < out[j].Encode().String() })
	return out, nil
}

// allKnownPartitions unions catalog discovery with the store's touched set.
func (s *Store) allKnownPartitions(ctx context.Context) ([]Partition, error) {
	discovered, err := s.catalog.Partitions(ctx)
	if err != nil {
		return nil, err
	}

	set := map[Partition]struct{}{}
	for _, p := range discovered {
		set[p] = struct{}{}
	}
	s.mu.Lock()
	for p := range s.known {
		set[p] = struct{}{}
	}
	s.mu.Unlock()

	partitions := make([]Partition, 0, len(set))
	for p := range set {
		partitions = append(partitions, p)
	}
	return partitions, nil
}

// scanAggregates returns the distinct aggregate ids in a partition, honouring a
// ScanType narrowing.
func scanAggregates(ctx context.Context, db *sql.DB, plan ReadPlan) ([]we.AggregateId, error) {
	query := `SELECT DISTINCT aggregate_type, aggregate_key FROM events`
	args := []any{}
	if plan.kind == readScanType {
		query += ` WHERE aggregate_type = ?`
		args = append(args, plan.aggregateType)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: failed to enumerate aggregates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []we.AggregateId
	for rows.Next() {
		var t, k string
		if err := rows.Scan(&t, &k); err != nil {
			return nil, fmt.Errorf("sqlite: failed to scan aggregate id: %w", err)
		}
		ids = append(ids, we.AggregateId{Type: t, Key: k})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: failed to read aggregate ids: %w", err)
	}
	return ids, nil
}
