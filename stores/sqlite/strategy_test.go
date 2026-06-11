package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/weegigs/wee-events-go/we"
)

func TestGlobalStrategyRoutesEverythingToDefault(t *testing.T) {
	s := Global()
	p := s.PartitionFor(we.AggregateId{Type: "order", Key: "1"})
	assert.True(t, p.IsDefault())
	assert.Equal(t, ScanAll().kind, s.ReadPlan(p).kind)
}

func TestByTypePartitionsByAggregateType(t *testing.T) {
	s := ByType()
	p := s.PartitionFor(we.AggregateId{Type: "order", Key: "1"})
	assert.Equal(t, "order", p.Name())

	plan := s.ReadPlan(p)
	assert.Equal(t, ScanType("order").kind, plan.kind)
	assert.Equal(t, "order", plan.aggregateType)

	back, err := s.PartitionFromName("order")
	require.NoError(t, err)
	assert.Equal(t, p, back)
}

func TestByAggregateRoundTrip(t *testing.T) {
	s := ByAggregate()
	id := we.AggregateId{Type: "order", Key: "abc"}
	p := s.PartitionFor(id)
	assert.Equal(t, "order:abc", p.Name())

	plan := s.ReadPlan(p)
	assert.Equal(t, Direct(id).kind, plan.kind)
	assert.Equal(t, id, plan.id)

	back, err := s.PartitionFromName("order:abc")
	require.NoError(t, err)
	assert.Equal(t, p, back)
}

func TestPartitionByUsesClosure(t *testing.T) {
	s := PartitionBy(func(id we.AggregateId) string { return id.Type + "-shard" })
	p := s.PartitionFor(we.AggregateId{Type: "order", Key: "1"})
	assert.Equal(t, "order-shard", p.Name())
}

// Name round-trips for every grammar-v2 identity (ADR-0010).
func TestByTypeNameRoundTripsProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		typ := we.IdentityTypeGen().Draw(rt, "type")
		s := ByType()
		p := s.PartitionFor(we.AggregateId{Type: typ, Key: "x"})
		back, err := s.PartitionFromName(s.PartitionName(p))
		require.NoError(rt, err)
		assert.Equal(rt, p, back)
	})
}
