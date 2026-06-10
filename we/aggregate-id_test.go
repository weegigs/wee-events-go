package we

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IDENTITY-S1.R1–R5 — the validating constructor and its closed reason set.
func TestMakeAggregateId(t *testing.T) {
	t.Run("valid identity round-trips the parts", func(t *testing.T) {
		id, err := MakeAggregateId("customer", "0042")
		require.NoError(t, err)
		assert.Equal(t, AggregateId{Type: "customer", Key: "0042"}, id)
	})

	t.Run("the full charsets are accepted", func(t *testing.T) {
		id, err := MakeAggregateId("Order-line_v2.x~y", "01HX-abc_2026.06.10~final")
		require.NoError(t, err)
		assert.Equal(t, "01HX-abc_2026.06.10~final", id.Key)
	})

	t.Run("composite keys use the pipe convention", func(t *testing.T) {
		id, err := MakeAggregateId("inventory", "kevin|card|boots")
		require.NoError(t, err)
		assert.Equal(t, "kevin|card|boots", id.Key, "the key is opaque — segments are never parsed")
	})

	cases := []struct {
		name, typ, key, reason string
	}{
		{"empty type", "", "k", ReasonEmptyType},
		{"empty key", "customer", "", ReasonEmptyKey},
		{"colon in type", "customer:evil", "k", ReasonInvalidType},
		{"colon in key", "customer", "tenant:42", ReasonInvalidKey},
		{"pipe in type", "customer|evil", "k", ReasonInvalidType},
		{"space in key", "customer", "has space", ReasonInvalidKey},
		{"percent in key", "customer", "a%20b", ReasonInvalidKey},
		{"nats wildcard in key", "customer", "a*b", ReasonInvalidKey},
		{"slash in type", "cust/omer", "k", ReasonInvalidType},
		{"dot segment key", "customer", "..", ReasonInvalidKey},
		{"single dot type", ".", "k", ReasonInvalidType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := MakeAggregateId(tc.typ, tc.key)
			require.Error(t, err)
			assert.Equal(t, AggregateId{}, id)
			var invalid *InvalidAggregateIdError
			require.True(t, errors.As(err, &invalid), "expected *InvalidAggregateIdError, got %T", err)
			assert.Equal(t, tc.reason, invalid.Reason)
			assert.Equal(t, tc.typ, invalid.Type)
			assert.Equal(t, tc.key, invalid.Key)
		})
	}
}
