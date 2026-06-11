package sqlite

import (
	"fmt"
	"strings"

	"github.com/weegigs/wee-events-go/we"
)

// PartitionStrategy derives a partition from an aggregate id, names it stably
// for file/namespace layout, recovers it from that name during discovery, and
// supplies the read plan enumeration uses. Mirrors the Rust PartitionStrategy.
type PartitionStrategy interface {
	PartitionFor(id we.AggregateId) Partition
	PartitionName(p Partition) string
	PartitionFromName(name string) (Partition, error)
	ReadPlan(p Partition) ReadPlan
}

// Marker interfaces gate which strategies are legal with which backend
// constructor, replacing the Rust type-state builder. A strategy that produces
// the default partition satisfies SingleTargetStrategy; one that produces named
// partitions satisfies NamingStrategy; LocalStrategy covers both file layouts.
type LocalStrategy interface{ PartitionStrategy }
type SingleTargetStrategy interface{ PartitionStrategy }
type NamingStrategy interface{ PartitionStrategy }

// global routes every aggregate to the default partition: one database.
type global struct{}

func Global() *global { return &global{} }

func (g *global) PartitionFor(we.AggregateId) Partition { return DefaultPartition() }
func (g *global) PartitionName(Partition) string        { return "" }
func (g *global) PartitionFromName(string) (Partition, error) {
	return DefaultPartition(), nil
}
func (g *global) ReadPlan(Partition) ReadPlan { return ScanAll() }

// byType partitions by aggregate type. The type string is the partition name;
// grammar v2 guarantees it is lowercase kebab, safe for files and namespaces.
type byType struct{}

func ByType() *byType { return &byType{} }

func (b *byType) PartitionFor(id we.AggregateId) Partition { return MakePartition(id.Type) }
func (b *byType) PartitionName(p Partition) string         { return p.Name() }
func (b *byType) PartitionFromName(name string) (Partition, error) {
	if name == "" {
		return Partition{}, fmt.Errorf("sqlite: empty partition name for by-type strategy")
	}
	return MakePartition(name), nil
}
func (b *byType) ReadPlan(p Partition) ReadPlan { return ScanType(p.Name()) }

// byAggregate partitions per aggregate. The name is the encoded "type:key", so
// the read plan answers Direct without a scan.
type byAggregate struct{}

func ByAggregate() *byAggregate { return &byAggregate{} }

func (b *byAggregate) PartitionFor(id we.AggregateId) Partition {
	return MakePartition(id.Encode().String())
}
func (b *byAggregate) PartitionName(p Partition) string { return p.Name() }
func (b *byAggregate) PartitionFromName(name string) (Partition, error) {
	if _, err := we.EncodedAggregateId(name).Decode(); err != nil {
		return Partition{}, fmt.Errorf("sqlite: invalid by-aggregate partition name %q: %w", name, err)
	}
	return MakePartition(name), nil
}
func (b *byAggregate) ReadPlan(p Partition) ReadPlan {
	id, err := we.EncodedAggregateId(p.Name()).Decode()
	if err != nil {
		// A partition whose name does not decode cannot name an aggregate; it is
		// pruned from enumeration rather than failing the whole scan.
		return Skip()
	}
	return Direct(id)
}

// partitionBy routes via a caller closure. Names are whatever the closure emits;
// the read plan is the conservative ScanAll because the mapping is opaque.
type partitionBy struct {
	fn func(we.AggregateId) string
}

func PartitionBy(fn func(we.AggregateId) string) *partitionBy { return &partitionBy{fn: fn} }

func (b *partitionBy) PartitionFor(id we.AggregateId) Partition {
	return MakePartition(strings.TrimSpace(b.fn(id)))
}
func (b *partitionBy) PartitionName(p Partition) string { return p.Name() }
func (b *partitionBy) PartitionFromName(name string) (Partition, error) {
	if name == "" {
		return Partition{}, fmt.Errorf("sqlite: empty partition name for partition-by strategy")
	}
	return MakePartition(name), nil
}
func (b *partitionBy) ReadPlan(Partition) ReadPlan { return ScanAll() }
