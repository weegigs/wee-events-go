package we

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The conformance contract (documents/spec/aggregate-identity.md
// §Conformance): every vector asserted, valid parse vectors round-trip
// byte-for-byte, and the expected file version is pinned so a grammar bump
// fails visibly here.
const (
	identityVectorsPath    = "../documents/spec/aggregate-identity.vectors.json"
	identityVectorsVersion = 1
)

type identityVectorFile struct {
	Spec      string            `json:"spec"`
	Version   int               `json:"version"`
	Construct []constructVector `json:"construct"`
	Parse     []parseVector     `json:"parse"`
}

type constructVector struct {
	Type   string `json:"type"`
	Key    string `json:"key"`
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

type parseVector struct {
	Input  string `json:"input"`
	Valid  bool   `json:"valid"`
	Type   string `json:"type,omitempty"`
	Key    string `json:"key,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func loadIdentityVectors(t *testing.T) identityVectorFile {
	t.Helper()
	raw, err := os.ReadFile(identityVectorsPath)
	require.NoError(t, err)
	var file identityVectorFile
	require.NoError(t, json.Unmarshal(raw, &file))
	require.Equal(t, "aggregate-identity", file.Spec)
	require.Equal(t, identityVectorsVersion, file.Version, "vector file version moved — update this test against the new grammar")
	require.NotEmpty(t, file.Construct)
	require.NotEmpty(t, file.Parse)
	return file
}

func TestIdentityConstructVectors(t *testing.T) {
	for _, v := range loadIdentityVectors(t).Construct {
		t.Run(v.Type+":"+v.Key, func(t *testing.T) {
			id, err := MakeAggregateId(v.Type, v.Key)
			if v.Valid {
				require.NoError(t, err)
				assert.Equal(t, AggregateId{Type: v.Type, Key: v.Key}, id)
				return
			}
			require.Error(t, err)
			var invalid *InvalidAggregateIdError
			require.True(t, errors.As(err, &invalid), "got %T", err)
			assert.Equal(t, v.Reason, invalid.Reason)
		})
	}
}

func TestIdentityParseVectors(t *testing.T) {
	for _, v := range loadIdentityVectors(t).Parse {
		t.Run(v.Input, func(t *testing.T) {
			id, err := EncodedAggregateId(v.Input).Decode()
			if v.Valid {
				require.NoError(t, err)
				assert.Equal(t, AggregateId{Type: v.Type, Key: v.Key}, id)
				assert.Equal(t, v.Input, id.Encode().String(), "valid parse vectors round-trip byte-for-byte")
				return
			}
			require.Error(t, err)
			var invalid *InvalidAggregateIdError
			require.True(t, errors.As(err, &invalid), "got %T", err)
			assert.Equal(t, v.Reason, invalid.Reason)
		})
	}
}
