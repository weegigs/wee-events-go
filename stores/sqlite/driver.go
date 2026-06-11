package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/weegigs/wee-events-go/we"
)

// ulidEntropy backs event_id generation. Event ids are ULIDs (26 chars) to
// satisfy the events.event_id CHECK(length(event_id) = 26). The monotonic
// entropy source is NOT goroutine-safe, so access is serialized — concurrent
// Publish calls would otherwise race on it (mirrors we.RevisionGenerator's
// own lock).
var (
	ulidLock    sync.Mutex
	ulidEntropy = ulid.Monotonic(rand.New(rand.NewSource(time.Now().UnixNano())), 0)
)

func newEventID() string {
	ulidLock.Lock()
	defer ulidLock.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), ulidEntropy).String()
}

// revisionForSequence encodes a 1-based per-aggregate sequence number as a
// 26-char hex revision, satisfying the events.revision CHECK(length = 26).
// Sequence 0 encodes to the 26 zeros of we.InitialRevision. Hex (not base-10)
// is the recorded encoding; the conformance suite's "past ten events" case
// guards the encode/decode base round-trip.
func revisionForSequence(sequence uint64) we.Revision {
	return we.Revision(fmt.Sprintf("%026x", sequence))
}

// timestampFromEventID derives a recorded event's timestamp from its ULID
// event id. An undecodable id is data corruption and fails the read — the
// store never fabricates a timestamp (SURFACE-S3.R1).
func timestampFromEventID(eventID string) (we.Timestamp, error) {
	id, err := ulid.Parse(eventID)
	if err != nil {
		return "", fmt.Errorf("sqlite: invalid event id %q: %w", eventID, err)
	}
	return we.TimestampFromTime(ulid.Time(id.Time())), nil
}

// sequenceForRevision decodes a hex revision back to its sequence number.
func sequenceForRevision(revision we.Revision) (uint64, error) {
	sequence, err := strconv.ParseUint(revision.String(), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("sqlite: invalid revision %q: %w", revision.String(), err)
	}
	return sequence, nil
}

// redactToken strips a known auth-token value from an error's text so a driver
// error that echoes the remote connection string cannot leak the credential
// into a wrapped error or a log. Returns err unchanged when no token is set.
//
// When the token is found, the result is a fresh errors.New — deliberately
// severing the wrap chain, because the original chain is exactly what carries
// the token. Callers lose errors.Is/As on the cause; the trade-off is confined
// to the constructor path, where no caller classifies the error.
func redactToken(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	scrubbed := strings.ReplaceAll(err.Error(), token, "REDACTED")
	if scrubbed == err.Error() {
		return err
	}
	return errors.New(scrubbed)
}

// applyBusyTimeout sets busy_timeout on one connection. Unlike WAL it is a
// per-connection SQLite setting, so every connection acquired from the
// database/sql pool for writing must apply it itself — a pragma issued on one
// pooled connection does not exist on the next.
func applyBusyTimeout(ctx context.Context, conn *sql.Conn, busyTimeout time.Duration) error {
	ms := busyTimeout.Milliseconds()
	var applied int64
	if err := conn.QueryRowContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", ms)).Scan(&applied); err != nil {
		return fmt.Errorf("sqlite: failed to set busy_timeout: %w", err)
	}

	return nil
}

// isUniqueViolation reports whether err is a violation of the aggregate
// concurrency index specifically — SQLite names the violated columns, so the
// optimistic-concurrency guard is distinguished from any other UNIQUE failure
// (e.g. an event_id primary-key collision, which is a broken id source, not a
// revision race). The go-libsql driver surfaces engine failures as a plain
// formatted error whose message embeds the SQLite text, so detection is by
// message content — the only signal the driver exposes.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(),
		"UNIQUE constraint failed: events.aggregate_type, events.aggregate_key, events.revision")
}

// isBusy reports whether err is a transient SQLite busy/locked failure that a
// bounded retry may clear. It must never match a UNIQUE-constraint violation.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database is busy") ||
		strings.Contains(msg, "SQLITE_BUSY")
}
