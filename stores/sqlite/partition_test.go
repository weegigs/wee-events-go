package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/weegigs/wee-events-go/we"
)

func TestPartitionDefaultIsDistinctFromNamed(t *testing.T) {
	def := DefaultPartition()
	named := MakePartition("bucket-3")

	assert.True(t, def.IsDefault())
	assert.False(t, named.IsDefault())
	assert.Equal(t, "bucket-3", named.Name())
	assert.NotEqual(t, def, named)
}

func TestPartitionsAreMapKeys(t *testing.T) {
	m := map[Partition]int{}
	m[MakePartition("a")] = 1
	m[MakePartition("a")] = 2
	m[DefaultPartition()] = 3

	assert.Len(t, m, 2)
	assert.Equal(t, 2, m[MakePartition("a")])
}

func TestReadPlanVariants(t *testing.T) {
	assert.Equal(t, readScanAll, ScanAll().kind)
	assert.Equal(t, "order", ScanType("order").aggregateType)
	assert.Equal(t, "order:1", Direct(we_AggregateId("order", "1")).id.Encode().String())
	assert.Equal(t, readSkip, Skip().kind)
}

func we_AggregateId(t, k string) we.AggregateId { return we.AggregateId{Type: t, Key: k} }
