package sqlite

import (
	"testing"
)

func TestSQLiteShardFanoutBenchmarkIDsMapToExpectedPartitions(t *testing.T) {
	strategy := fanoutByTypeStrategy()

	oneShard := map[string]struct{}{}
	for _, id := range fanoutOneShardIDs(100) {
		oneShard[strategy.PartitionFor(id).Name()] = struct{}{}
	}
	if len(oneShard) != 1 {
		t.Fatalf("one-shard benchmark ids mapped to %d partitions, want 1", len(oneShard))
	}

	manyShards := map[string]struct{}{}
	for _, id := range fanoutManyShardIDs(100) {
		manyShards[strategy.PartitionFor(id).Name()] = struct{}{}
	}
	if len(manyShards) != 100 {
		t.Fatalf("many-shards benchmark ids mapped to %d partitions, want 100", len(manyShards))
	}
}
