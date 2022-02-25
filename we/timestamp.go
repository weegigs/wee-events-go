package we

import "time"

type Timestamp string

const RFC3339Milli = "2006-01-02T15:04:05.999Z07:00"

func TimestampFromTime(t time.Time) Timestamp {
	return Timestamp(t.UTC().Format(RFC3339Milli))
}
