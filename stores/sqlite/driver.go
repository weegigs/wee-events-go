package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
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
// event id, which encodes the creation time. An event id that is not a
// parseable ULID yields the zero timestamp rather than panicking — the store
// stays codec/identifier-agnostic on the read path.
func timestampFromEventID(eventID string) we.Timestamp {
	id, err := ulid.Parse(eventID)
	if err != nil {
		return we.Timestamp("")
	}
	return we.TimestampFromTime(ulid.Time(id.Time()))
}

// sequenceForRevision decodes a hex revision back to its sequence number.
func sequenceForRevision(revision we.Revision) (uint64, error) {
	sequence, err := strconv.ParseUint(revision.String(), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("sqlite: invalid revision %q: %w", revision.String(), err)
	}
	return sequence, nil
}

// libsqlOption carries a driver-level setting that the go-libsql DSN encodes
// as a query parameter (the standard database/sql entry point has no typed
// option API). Only the remote auth token is supported in this cut.
type libsqlOption struct {
	key   string
	value string
}

func withAuthToken(token string) libsqlOption {
	return libsqlOption{key: "authToken", value: token}
}

// redactToken strips a known auth-token value from an error's text so a driver
// error that echoes the remote connection string cannot leak the credential
// into a wrapped error or a log. Returns err unchanged when no token is set.
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

// applyLibsqlOptions folds driver options into the DSN. The go-libsql remote
// connector reads authToken from the URL query string. Local and in-memory
// DSNs accept no options in this cut, so any option there is an error rather
// than a silently ignored setting.
func applyLibsqlOptions(dsn string, options []libsqlOption) (string, error) {
	if len(options) == 0 {
		return dsn, nil
	}

	if !isRemoteDSN(dsn) {
		return "", fmt.Errorf("sqlite: connection options are only supported for remote targets")
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("sqlite: invalid remote url: %w", err)
	}

	query := u.Query()
	for _, option := range options {
		query.Set(option.key, option.value)
	}
	u.RawQuery = query.Encode()

	return u.String(), nil
}

func isRemoteDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "libsql://") ||
		strings.HasPrefix(dsn, "https://") ||
		strings.HasPrefix(dsn, "http://")
}

// applyPragmas sets the store-level PRAGMAs at construction. journal_mode and
// busy_timeout return their resulting value, so they must run through QueryRow
// rather than Exec (the go-libsql driver rejects an Exec that returns rows).
// WAL is a database-level property persisted in the file header, so setting it
// once covers every connection; it is a no-op for the in-memory target (which
// reports "memory").
func applyPragmas(ctx context.Context, conn *sql.Conn, busyTimeout time.Duration) error {
	var journalMode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
		return fmt.Errorf("sqlite: failed to set journal_mode: %w", err)
	}

	return applyBusyTimeout(ctx, conn, busyTimeout)
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

// isUniqueViolation reports whether err is a SQLite UNIQUE-constraint failure.
// The go-libsql driver surfaces engine failures as a plain formatted error
// whose message embeds the SQLite text, so detection is by message content —
// the only signal the driver exposes.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
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
