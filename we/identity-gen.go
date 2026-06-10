package we

import "pgregory.net/rapid"

// IdentityTypeGen generates aggregate types across the full identity charset
// grammar (IDENTITY-S1.R4, ADR-0009).
func IdentityTypeGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9._~-]{1,64}`).
		Filter(func(s string) bool { return s != "." && s != ".." })
}

// IdentityKeyGen generates aggregate keys across the full key charset grammar,
// including the '|' composite-segment separator (IDENTITY-S1.R8, ADR-0009).
func IdentityKeyGen() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9._~|-]{1,128}`).
		Filter(func(s string) bool { return s != "." && s != ".." })
}
