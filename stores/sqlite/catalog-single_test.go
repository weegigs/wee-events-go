package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSingleTargetReturnsOneTargetForEveryPartition(t *testing.T) {
	ctx := context.Background()
	cat := newSingleTargetCatalog(Target{dsn: ":memory:"})

	a, err := cat.EnsureTarget(ctx, DefaultPartition())
	require.NoError(t, err)
	b, err := cat.EnsureTarget(ctx, MakePartition("anything"))
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

func TestSingleTargetEnumeratesEmpty(t *testing.T) {
	ctx := context.Background()
	cat := newSingleTargetCatalog(Target{dsn: ":memory:"})
	parts, err := cat.Partitions(ctx)
	require.NoError(t, err)
	assert.Empty(t, parts)
}

func TestSingleTargetExistingAlwaysPresent(t *testing.T) {
	ctx := context.Background()
	cat := newSingleTargetCatalog(Target{dsn: ":memory:"})
	_, ok, err := cat.ExistingTarget(ctx, DefaultPartition())
	require.NoError(t, err)
	assert.True(t, ok)
}
