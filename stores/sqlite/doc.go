// Package sqlite implements the we.EventStore contract on top of a
// SQLite/libSQL database. It is an events-only store: a single events table
// holds one row per recorded event, and optimistic concurrency is enforced by
// a UNIQUE index on (aggregate_type, aggregate_key, revision). The store never
// interprets payload bytes — the data column holds the opaque encoded payload
// and the encoding column holds its discriminator verbatim, so whatever codec
// wrote an event round-trips unchanged (single-responsibility seam, principle
// 1).
//
// # Driver and target matrix
//
// The store uses github.com/tursodatabase/go-libsql (registered as the
// "libsql" database/sql driver). This is the official libSQL driver and the
// single driver chosen in ADR-0003 because it covers all three targets with
// one connection model and one SQL engine, giving the strongest behaviour
// parity between local and remote. It links the libSQL C library and therefore
// requires cgo (CGO_ENABLED=1, a C toolchain, and one of the prebuilt
// platforms below); the cgo dependency is confined to this package so the rest
// of the framework remains buildable with CGO_ENABLED=0.
//
//	Target            Constructor option   DSN form
//	----------------  -------------------   ------------------------------
//	in-memory         InMemory()            :memory:
//	local file        LocalFile(path)       file:<path>
//	remote sqld/Turso Remote(url, token)    libsql://… | https://… | http://…
//
// go-libsql ships prebuilt native libraries for darwin/arm64, linux/amd64,
// and linux/arm64 only; the package cannot be built for other
// platforms or with cgo disabled. Turso Platform provisioning (creating remote
// databases via the Platform API) is a later phase and is not part of this
// package.
//
// # Lifecycle
//
// NewStore returns a usable *Store or an error — never a half-built store
// (principle 2). Each *Store owns its own *sql.DB connection pool and must be
// released through Close. Two stores opened over the same local file observe
// each other's committed events (shared-backing parity); an in-memory store is
// private to its single owning *Store.
//
// # Known limitation: remote commit acknowledgement loss
//
// On a Remote target, a COMMIT that succeeds server-side but whose
// acknowledgement is lost in transit surfaces as an error. If that error text
// matches the transient-busy classifier the publish is retried with the same
// pre-generated event ids and reports a spurious we.RevisionConflict; if it
// does not, the raw error is returned. Both outcomes are safe — the batch is
// never written twice and never silently lost — but a caller may observe a
// conflict (or an error) for a batch that actually committed.
package sqlite
