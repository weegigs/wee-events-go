package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/weegigs/wee-events-go/we"
)

func TestHashedFNVMatchesRust(t *testing.T) {
	cases := []struct {
		typ, key string
		hash     uint32
	}{
		{"order", "abc", 1743764345},
		{"order", "xyz", 2324409474},
		{"user", "kevin", 99363673},
		{"campaign", "123", 161394085},
		{"widget", "42", 1608878411},
	}
	for _, c := range cases {
		assert.Equalf(t, c.hash, fnv1aAggregate(we.AggregateId{Type: c.typ, Key: c.key}),
			"hash mismatch for %s:%s", c.typ, c.key)
	}
}

func TestHashedBucketAssignment(t *testing.T) {
	s := Hashed(8)
	// campaign:123 hashes to 161394085; 161394085 % 8 == 5.
	p := s.PartitionFor(we.AggregateId{Type: "campaign", Key: "123"})
	assert.Equal(t, "bucket-5", p.Name())
	assert.Equal(t, ScanAll().kind, s.ReadPlan(p).kind)
}

func TestHashedNameRoundTrip(t *testing.T) {
	s := Hashed(16)
	p := s.PartitionFor(we.AggregateId{Type: "order", Key: "xyz"})
	back, err := s.PartitionFromName(s.PartitionName(p))
	require.NoError(t, err)
	assert.Equal(t, p, back)
}

func TestHashedRejectsZeroBuckets(t *testing.T) {
	assert.Panics(t, func() { Hashed(0) })
}
