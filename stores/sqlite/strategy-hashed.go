package sqlite

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

const (
	fnvOffset32 uint32 = 0x811c9dc5
	fnvPrime32  uint32 = 0x01000193
)

// fnv1aAggregate hashes "type:key" with 32-bit FNV-1a. It reproduces the Rust
// hash_aggregate_id byte-for-byte so an aggregate lands in the same bucket in
// both implementations (cross-implementation layout parity).
func fnv1aAggregate(id we.AggregateId) uint32 {
	hash := fnvOffset32
	mix := func(b byte) {
		hash ^= uint32(b)
		hash *= fnvPrime32
	}
	for i := 0; i < len(id.Type); i++ {
		mix(id.Type[i])
	}
	mix(':')
	for i := 0; i < len(id.Key); i++ {
		mix(id.Key[i])
	}
	return hash
}

// hashed shards into a fixed number of buckets named "bucket-<i>".
type hashed struct {
	buckets uint32
}

// Hashed builds a bucketed strategy. A zero bucket count is a programming error
// (division by zero), reported as a panic at construction, not a deferred fault.
func Hashed(buckets uint32) *hashed {
	if buckets == 0 {
		panic("sqlite: Hashed requires a non-zero bucket count")
	}
	return &hashed{buckets: buckets}
}

func (h *hashed) PartitionFor(id we.AggregateId) Partition {
	bucket := fnv1aAggregate(id) % h.buckets
	return MakePartition(fmt.Sprintf("bucket-%d", bucket))
}
func (h *hashed) PartitionName(p Partition) string { return p.Name() }
func (h *hashed) PartitionFromName(name string) (Partition, error) {
	digits, ok := strings.CutPrefix(name, "bucket-")
	if !ok {
		return Partition{}, fmt.Errorf("sqlite: invalid hashed partition name %q", name)
	}
	if _, err := strconv.ParseUint(digits, 10, 32); err != nil {
		return Partition{}, fmt.Errorf("sqlite: invalid hashed partition name %q: %w", name, err)
	}
	return MakePartition(name), nil
}
func (h *hashed) ReadPlan(Partition) ReadPlan { return ScanAll() }
