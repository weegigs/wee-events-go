package es

import (
  "math/rand"
  "sync"
  "time"

  "github.com/oklog/ulid/v2"
)

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

func (revision Revision) Timestamp() Timestamp {
  v := ulid.MustParse(string(revision))
  return Timestamp(ulid.Time(v.Time()).UTC().Format(RFC3339Milli))
}

func (revision Revision) String() string {
  return string(revision)
}
