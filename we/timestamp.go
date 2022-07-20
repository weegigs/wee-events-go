package we

import "time"

type Timestamp string

const RFC3339Milli = "2006-01-02T15:04:05.999Z07:00"

func TimestampFromTime(t time.Time) Timestamp {
  return Timestamp(t.UTC().Format(RFC3339Milli))
}

func (t Timestamp) String() string {
  return string(t)
}

func (t Timestamp) Time() (time.Time, error) {
  return time.Parse(RFC3339Milli, t.String())
}
