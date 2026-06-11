package sqlite

import "github.com/weegigs/wee-events-go/we"

// Partition is a logical storage partition. It is a comparable value so it can
// key the store's shard map. The default partition is the single-database case
// (GlobalStrategy); named partitions carry a stable, strategy-derived name used
// for files and namespaces. Mirrors Rust PartitionName::Default | Named.
type Partition struct {
	name      string
	isDefault bool
}

// DefaultPartition is the singleton partition used by single-database strategies.
func DefaultPartition() Partition { return Partition{isDefault: true} }

// MakePartition builds a named partition.
func MakePartition(name string) Partition { return Partition{name: name} }

func (p Partition) Name() string    { return p.name }
func (p Partition) IsDefault() bool { return p.isDefault }

type readKind int

const (
	readScanAll  readKind = iota
	readScanType readKind = iota
	readDirect   readKind = iota
	readSkip     readKind = iota
)

// ReadPlan tells enumeration how to harvest aggregate ids from a partition:
// ScanAll reads every aggregate, ScanType narrows to one aggregate type, Direct
// names a single aggregate without a query, and Skip omits the partition.
type ReadPlan struct {
	kind          readKind
	aggregateType string
	id            we.AggregateId
}

func ScanAll() ReadPlan                 { return ReadPlan{kind: readScanAll} }
func ScanType(t string) ReadPlan        { return ReadPlan{kind: readScanType, aggregateType: t} }
func Direct(id we.AggregateId) ReadPlan { return ReadPlan{kind: readDirect, id: id} }
func Skip() ReadPlan                    { return ReadPlan{kind: readSkip} }
