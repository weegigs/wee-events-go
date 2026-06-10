package we

import "pgregory.net/rapid"

// IdentityTypeGen generates aggregate types across the type grammar —
// kebab-case tokens, letter-first (documents/spec/aggregate-identity.md,
// ADR-0009). Bounded well inside the 64-octet cap; the cap boundary is
// pinned by the conformance vectors, not the generator.
func IdentityTypeGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-z][a-z0-9]{0,9}(-[a-z0-9]{1,10}){0,3}`)
}

// IdentityKeyGen generates aggregate keys across the key grammar, including
// pipe-joined composite segments (documents/spec/aggregate-identity.md,
// ADR-0009). Bounded well inside the 512-octet cap; the whole-key dot rule
// is the only filter the grammar shape cannot express.
func IdentityKeyGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9._@-]{1,16}(\|[A-Za-z0-9._@-]{1,16}){0,5}`).
		Filter(func(s string) bool { return s != "." && s != ".." })
}
