package sqlite

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// stableHashHex must match wee-events.rs stable_hash_hex (FNV-1a 64) so both
// implementations derive identical platform database names. The literals are
// the published FNV-1a 64 test vectors.
func TestStableHashHexMatchesFNV1a64Vectors(t *testing.T) {
	assert.Equal(t, "cbf29ce484222325", stableHashHex(""))
	assert.Equal(t, "af63dc4c8601ec8c", stableHashHex("a"))
}

func TestNamedDatabasePrefixCombinesFragmentAndHash(t *testing.T) {
	assert.Equal(t, "myapp-"+stableHashHex("myapp")[:8], namedDatabasePrefix("myapp"))
}

func TestNamedDatabasePrefixTruncatesLongPrefixFragments(t *testing.T) {
	prefix := strings.Repeat("a", 50)
	got := namedDatabasePrefix(prefix)
	assert.Equal(t, strings.Repeat("a", 12)+"-"+stableHashHex(prefix)[:8], got)
}

// The cases below mirror the wee-events.rs sanitize.rs test suite so the two
// implementations stay byte-compatible for shared Turso organisations.
func TestSanitizeDatabaseNameMatchesRustBehaviour(t *testing.T) {
	cases := []struct {
		name      string
		partition string
		prefix    string
		want      string
	}{
		{"simple lowercase", "orders", "myapp",
			namedDatabasePrefix("myapp") + "-orders-" + stableHashHex("orders")},
		{"uppercase lowercased", "Orders", "myapp",
			namedDatabasePrefix("myapp") + "-orders-" + stableHashHex("Orders")},
		{"special chars become dashes", "tenant:acme/us-east", "myapp",
			namedDatabasePrefix("myapp") + "-tenant-acme-us-east-" + stableHashHex("tenant:acme/us-east")},
		{"underscores become dashes", "my_db", "app",
			namedDatabasePrefix("app") + "-my-db-" + stableHashHex("my_db")},
		{"consecutive dashes collapse", "a--b", "myapp",
			namedDatabasePrefix("myapp") + "-a-b-" + stableHashHex("a--b")},
		{"trailing specials stripped", "trail:", "myapp",
			namedDatabasePrefix("myapp") + "-trail-" + stableHashHex("trail:")},
		{"empty partition returns sanitized prefix", "", "myapp", "myapp"},
		{"default partition sanitizes prefix", "", "My_App:", "my-app"},
		{"mixed case complex", "Tenant:ACME/US_East", "ev",
			namedDatabasePrefix("ev") + "-tenant-acme-us-east-" + stableHashHex("Tenant:ACME/US_East")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sanitizeDatabaseName(tc.partition, tc.prefix))
		})
	}
}

func TestSanitizeDatabaseNameStaysWithinTursoLimit(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := sanitizeDatabaseName(long, "myapp")
	assert.LessOrEqual(t, len(got), 51)
	assert.True(t, strings.HasPrefix(got, namedDatabasePrefix("myapp")+"-"))
}

func TestSanitizeDatabaseNameDropsPartitionWhenPrefixEatsBudget(t *testing.T) {
	prefix := strings.Repeat("a", 50)
	got := sanitizeDatabaseName("b", prefix)
	assert.LessOrEqual(t, len(got), 51)
	assert.True(t, strings.HasSuffix(got, stableHashHex("b")))
}

// Distinct partition names whose lossy fragments coincide must still map to
// distinct database names: the identity grammar allows '.', '_', '@', '|',
// ':' and mixed case in keys, all of which the fragment collapses to '-'.
func TestSanitizeDatabaseNameDistinguishesCollidingFragments(t *testing.T) {
	names := []string{"foo:a.b", "foo:a_b", "foo:a@b", "foo:a-b", "foo:A-b"}
	seen := map[string]string{}
	for _, name := range names {
		db := sanitizeDatabaseName(name, "we")
		prev, dup := seen[db]
		assert.False(t, dup, "%q and %q collide on %q", prev, name, db)
		seen[db] = name
	}
}

func TestSqldNamespaceDistinguishesCollidingFragments(t *testing.T) {
	p := newSqldProvisioner("http://admin.local", "libsql://data.local", "")
	names := []string{"foo:a.b", "foo:a_b", "foo:a@b", "foo:a-b", "foo:A-b"}
	seen := map[string]string{}
	for _, name := range names {
		ns := p.namespace(PartitionName{name: name})
		prev, dup := seen[ns]
		assert.False(t, dup, "%q and %q collide on %q", prev, name, ns)
		seen[ns] = name
	}
}

func TestSqldNamespaceKeepsReadableFragmentWithHashSuffix(t *testing.T) {
	p := newSqldProvisioner("http://admin.local", "libsql://data.local", "")
	assert.Equal(t, "default", p.namespace(PartitionName{isDefault: true}))
	assert.Equal(t, "order-abc-def-"+stableHashHex("order:abc_def"),
		p.namespace(PartitionName{name: "order:abc_def"}))
}
