package we

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// IDENTITY-S1 — the validating constructor and its closed reason set
// (normative grammar: documents/spec/aggregate-identity.md).
func TestMakeAggregateId(t *testing.T) {
	t.Run("valid identity round-trips the parts", func(t *testing.T) {
		id, err := MakeAggregateId("customer", "0042")
		require.NoError(t, err)
		assert.Equal(t, AggregateId{Type: "customer", Key: "0042"}, id)
	})

	t.Run("the full grammar is accepted", func(t *testing.T) {
		id, err := MakeAggregateId("order-line-v2", "01HX-abc_2026.06.10@final")
		require.NoError(t, err)
		assert.Equal(t, "01HX-abc_2026.06.10@final", id.Key)
	})

	t.Run("composite keys use the pipe convention", func(t *testing.T) {
		id, err := MakeAggregateId("inventory", "kevin|card|boots")
		require.NoError(t, err)
		assert.Equal(t, "kevin|card|boots", id.Key, "the key is opaque — segments are never parsed")
	})

	t.Run("length caps sit at 64 and 512 octets", func(t *testing.T) {
		_, err := MakeAggregateId("a"+strings.Repeat("b", 63), strings.Repeat("k", 512))
		require.NoError(t, err)
	})

	t.Run("interior dot segments are opaque data", func(t *testing.T) {
		for _, key := range []string{"a|..|b", "v1..2", ".well-known"} {
			_, err := MakeAggregateId("run", key)
			require.NoError(t, err, "key %q", key)
		}
	})

	cases := []struct {
		name, typ, key, reason string
	}{
		{"empty type", "", "k", ReasonEmptyType},
		{"empty key", "customer", "", ReasonEmptyKey},
		{"uppercase type", "Customer", "k", ReasonInvalidType},
		{"tilde in type", "a~b", "k", ReasonInvalidType},
		{"leading digit type", "2fa", "k", ReasonInvalidType},
		{"leading hyphen type", "-a", "k", ReasonInvalidType},
		{"trailing hyphen type", "a-", "k", ReasonInvalidType},
		{"doubled hyphen type", "a--b", "k", ReasonInvalidType},
		{"underscore in type", "a_b", "k", ReasonInvalidType},
		{"dot in type", "a.b", "k", ReasonInvalidType},
		{"at in type", "a@b", "k", ReasonInvalidType},
		{"pipe in type", "a|b", "k", ReasonInvalidType},
		{"colon in type", "customer:evil", "k", ReasonInvalidType},
		{"slash in type", "cust/omer", "k", ReasonInvalidType},
		{"over-long type", "a" + strings.Repeat("b", 64), "k", ReasonInvalidType},
		{"tilde in key", "customer", "k~v", ReasonInvalidKey},
		{"colon in key", "customer", "tenant:42", ReasonInvalidKey},
		{"space in key", "customer", "has space", ReasonInvalidKey},
		{"percent in key", "customer", "a%20b", ReasonInvalidKey},
		{"nats wildcard in key", "customer", "a*b", ReasonInvalidKey},
		{"whole-key single dot", "customer", ".", ReasonInvalidKey},
		{"whole-key dot segment", "customer", "..", ReasonInvalidKey},
		{"leading pipe key", "customer", "|k", ReasonInvalidKey},
		{"trailing pipe key", "customer", "k|", ReasonInvalidKey},
		{"doubled pipe key", "customer", "a||b", ReasonInvalidKey},
		{"non-ascii key", "customer", "café", ReasonInvalidKey},
		{"over-long key", "customer", strings.Repeat("k", 513), ReasonInvalidKey},
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

// IDENTITY-S2.R1 / IDENTITY-S3 — canonical form matches Rust Display; Decode
// is the exact inverse with the closed parse errors.
func TestCanonicalEncoding(t *testing.T) {
	t.Run("encodes type:key matching Rust Display", func(t *testing.T) {
		id := AggregateId{Type: "counter", Key: "live-1"}
		assert.Equal(t, EncodedAggregateId("counter:live-1"), id.Encode())
	})

	t.Run("round-trips identities across the key grammar", func(t *testing.T) {
		for _, key := range []string{"0042", "2026.06.10-17", "01HX_abc", "a-b.c_d@e", "kevin|card|boots", "a|..|b"} {
			id, err := MakeAggregateId("order", key)
			require.NoError(t, err)
			decoded, err := id.Encode().Decode()
			require.NoError(t, err)
			assert.Equal(t, id, decoded, "key %q", key)
		}
	})

	// IDENTITY-S3.R4 (ADR-0009) — generative round-trip over the full grammar,
	// with shrinking to minimal counterexamples.
	t.Run("property: every constructible identity round-trips", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			typ := IdentityTypeGen().Draw(rt, "type")
			key := IdentityKeyGen().Draw(rt, "key")
			id, err := MakeAggregateId(typ, key)
			require.NoError(rt, err)
			decoded, err := id.Encode().Decode()
			require.NoError(rt, err)
			require.Equal(rt, id, decoded)
		})
	})

	// IDENTITY-S3.R4 companion — injecting any out-of-charset rune into either
	// part is rejected with the correct closed-set reason. Placement rules for
	// in-charset runes are pinned by the vector file, not this property.
	t.Run("property: out-of-charset characters are rejected", func(t *testing.T) {
		rapid.Check(t, func(rt *rapid.T) {
			typ := IdentityTypeGen().Draw(rt, "type")
			key := IdentityKeyGen().Draw(rt, "key")

			part := rapid.SampledFrom([]string{"type", "key"}).Draw(rt, "part")
			target, charset, reason := key, identityKeyRunes, ReasonInvalidKey
			if part == "type" {
				target, charset, reason = typ, identityTypeRunes, ReasonInvalidType
			}

			bad := rapid.Rune().Filter(func(r rune) bool {
				return !strings.ContainsRune(charset, r)
			}).Draw(rt, "bad")
			pos := rapid.IntRange(0, len(target)).Draw(rt, "pos") // generator output is ASCII; byte positions are rune positions
			mutated := target[:pos] + string(bad) + target[pos:]

			if part == "type" {
				typ = mutated
			} else {
				key = mutated
			}

			_, err := MakeAggregateId(typ, key)
			require.Error(rt, err)
			var invalid *InvalidAggregateIdError
			require.True(rt, errors.As(err, &invalid), "got %T", err)
			require.Equal(rt, reason, invalid.Reason)
		})
	})

	decodeFailures := []struct {
		name, input, reason string
	}{
		{"missing separator", "no-colon-here", ReasonMissingSeparator},
		{"empty type", ":key", ReasonEmptyType},
		{"empty key", "type:", ReasonEmptyKey},
		{"empty string", "", ReasonMissingSeparator},
		{"second colon lands in the key", "type:a:b", ReasonInvalidKey},
		{"space in key", "type:a b", ReasonInvalidKey},
		{"uppercase type", "Counter:k", ReasonInvalidType},
	}
	for _, tc := range decodeFailures {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EncodedAggregateId(tc.input).Decode()
			require.Error(t, err)
			var invalid *InvalidAggregateIdError
			require.True(t, errors.As(err, &invalid))
			assert.Equal(t, tc.reason, invalid.Reason)
		})
	}
}
