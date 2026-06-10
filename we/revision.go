package we

import (
	"math/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Revision is an opaque per-store ordering token: incremental and
// lexicographically comparable within one aggregate's stream — nothing more.
// Its byte layout is store-specific (ULID-derived in some backends, fixed-
// width hex in others) and it is NOT convertible to a timestamp: a hex
// revision parses as plausible base32 and would yield a confident garbage
// date (SURFACE-S3.R3). Event times live on RecordedEvent.Timestamp.
type Revision string

const InitialRevision = Revision("00000000000000000000000000")

type RevisionGenerator struct {
	lk      sync.Mutex
	entropy *ulid.MonotonicEntropy
}

func NewRevisionGenerator() *RevisionGenerator {
	t := time.Now()
	entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0)

	return &RevisionGenerator{
		entropy: entropy,
	}
}

func (g *RevisionGenerator) NewRevision(t time.Time) Revision {
	g.lk.Lock()
	defer g.lk.Unlock()

	return Revision(ulid.MustNew(ulid.Timestamp(t), g.entropy).String())
}

func (revision Revision) String() string {
	return string(revision)
}
