package sqlite

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// Remote database naming, byte-compatible with wee-events.rs
// (crates/wee-events-sqlite/src/event_store/turso_platform/sanitize.rs).
//
// A partition name is reduced to a readable lowercase fragment plus a stable
// FNV-1a 64 hash of the original name. The fragment alone is lossy — the
// identity grammar allows '.', '_', '@', '|', ':' and mixed case in keys, all
// of which collapse to '-' — so the hash suffix is what keeps distinct
// partitions on distinct databases. Residual hash collisions are caught by
// the per-shard partition-metadata check (ensurePartitionName), which fails
// loudly rather than mixing partitions.

const tursoNameLimit = 51

// sanitizeDatabaseName builds the Turso platform database name for a
// partition. The default partition (empty name) uses the sanitized prefix
// alone; named partitions use "<prefix fragment>-<prefix hash8>-<partition
// fragment>-<partition hash16>" within Turso's 51-character limit.
func sanitizeDatabaseName(partitionName, prefix string) string {
	if partitionName == "" {
		return sanitizeFragment(prefix, tursoNameLimit)
	}

	namedPrefix := namedDatabasePrefix(prefix)
	partitionHash := stableHashHex(partitionName)
	remaining := tursoNameLimit - len(namedPrefix) - len(partitionHash) - 2
	fragment := sanitizeFragment(partitionName, remaining)
	if fragment == "" {
		return namedPrefix + "-" + partitionHash
	}
	return namedPrefix + "-" + fragment + "-" + partitionHash
}

// namedDatabasePrefix disambiguates the configured prefix the same way the
// partition is disambiguated: a short readable fragment plus a hash of the
// original prefix, so distinct prefixes that sanitize alike stay distinct.
func namedDatabasePrefix(prefix string) string {
	const fragmentLimit = 12
	const hashLen = 8

	fragment := sanitizeFragment(prefix, fragmentLimit)
	if fragment == "" {
		fragment = "db"
	}
	return fragment + "-" + stableHashHex(prefix)[:hashLen]
}

// sanitizeFragment lowercases and keeps [a-z0-9], collapses every other rune
// into a single '-', and never emits a leading or trailing dash. The result
// is capped at limit bytes.
func sanitizeFragment(raw string, limit int) string {
	var b strings.Builder
	prevDash := false

	for _, r := range raw {
		var out byte
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			prevDash = false
			out = byte(r)
		case r >= 'A' && r <= 'Z':
			prevDash = false
			out = byte(r) - 'A' + 'a'
		default:
			if prevDash || b.Len() == 0 {
				continue
			}
			prevDash = true
			out = '-'
		}
		if b.Len() >= limit {
			break
		}
		b.WriteByte(out)
	}

	return strings.TrimRight(b.String(), "-")
}

// stableHashHex is FNV-1a 64 rendered as 16 lowercase hex digits, matching
// wee-events.rs stable_hash_hex.
func stableHashHex(raw string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(raw))
	return fmt.Sprintf("%016x", h.Sum64())
}
