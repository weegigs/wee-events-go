package we

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCovertsToISODatetime(t *testing.T) {
	timestamp := string(InitialRevision.Timestamp())
	assert.Equal(t, timestamp, time.Unix(0, 0).UTC().Format(RFC3339Milli))

	now := time.Now()
	generator := NewRevisionGenerator()
	Revision := generator.NewRevision(now)

	timestamp = string(Revision.Timestamp())
	assert.Equal(t, now.UTC().Format(RFC3339Milli), timestamp)
}
