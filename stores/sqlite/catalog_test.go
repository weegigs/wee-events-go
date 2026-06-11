package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTargetDSNInMemory(t *testing.T) {
	tgt := Target{dsn: ":memory:"}
	assert.Equal(t, ":memory:", tgt.dsn)
	assert.Empty(t, tgt.authToken)
}

func TestTargetRedactionToken(t *testing.T) {
	tgt := Target{dsn: "libsql://db.turso.io", authToken: "secret"}
	assert.Equal(t, "secret", tgt.authToken)
}
