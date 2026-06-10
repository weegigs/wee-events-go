package we

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENCODING-S2.R3 / S2.R5 — the per-publish override takes precedence; an
// explicit nil override is an error, never a fallback.
func TestEncoderResolution(t *testing.T) {
	store := MakeJSONEncoder()

	t.Run("no override resolves the store encoder", func(t *testing.T) {
		enc, err := Options().EncoderFor(store)
		require.NoError(t, err)
		assert.Equal(t, store, enc)
	})

	t.Run("override takes precedence", func(t *testing.T) {
		override := MakeCBOREncoder()
		enc, err := Options(WithEncoder(override)).EncoderFor(store)
		require.NoError(t, err)
		assert.Equal(t, Encoder(override), enc)
	})

	t.Run("explicit nil override is an error", func(t *testing.T) {
		_, err := Options(WithEncoder(nil)).EncoderFor(store)
		require.ErrorIs(t, err, NilEncoder)
		assert.Contains(t, err.Error(), "encoder must not be nil")
	})
}
