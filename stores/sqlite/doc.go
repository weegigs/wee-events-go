// Package sqlite implements the we.EventStore contract on top of a
// SQLite/libSQL database. It is an events-only store: a single events table
// holds one row per recorded event, and optimistic concurrency is enforced by
// a UNIQUE index on (aggregate_type, aggregate_key, revision). The store never
// interprets payload bytes — the data column holds the opaque encoded payload
// and the encoding column holds its discriminator verbatim, so whatever codec
// wrote an event round-trips unchanged (single-responsibility seam, principle
// 1).
//
// # Partitioning
//
// The store is partitioned: a PartitionStrategy routes each aggregate to a
// logical Partition, a PartitionCatalog maps the partition to a concrete
// database target, and each partition is owned by exactly one shard goroutine
// that serialises every load and publish over a single pinned connection.
// Shards open lazily on first use. The bundled strategies are Global (one
// database for everything), ByType (one partition per aggregate type),
// ByAggregate (one partition per aggregate), Hashed (a fixed number of
// buckets), and PartitionBy (a caller-supplied routing closure).
//
// # Driver and backend matrix
//
// The store uses github.com/tursodatabase/go-libsql (registered as the
// "libsql" database/sql driver). This is the official libSQL driver and the
// single driver chosen in ADR-0003 because it covers every backend with one
// connection model and one SQL engine, giving the strongest behaviour parity
// between local and remote. It links the libSQL C library and therefore
// requires cgo (CGO_ENABLED=1, a C toolchain, and one of the prebuilt
// platforms below); the cgo dependency is confined to this package so the rest
// of the framework remains buildable with CGO_ENABLED=0.
//
// A backend pairs a strategy with the catalog that serves it. The backend
// constructors are:
//
//	Backend constructor                                Storage
//	-------------------------------------------------  -----------------------------
//	InMemory(strategy)                                 one private :memory: database
//	Local(root, strategy)                              local SQLite file(s) under root
//	SqldDefault(url, authToken, strategy)              one shared sqld endpoint
//	SqldNamespaced(adminURL, dataURL, authToken, ...)  one sqld namespace per partition
//	Turso(config, strategy)                            one Turso platform database per partition
//
// InMemory and SqldDefault accept only single-target strategies (Global);
// SqldNamespaced and Turso require a naming strategy; Local accepts either
// layout.
//
// go-libsql ships prebuilt native libraries for darwin/arm64, linux/amd64,
// and linux/arm64 only; the package cannot be built for other platforms or
// with cgo disabled. The Turso backend provisions one platform database per
// partition through the Platform API and awaits a new database's edge route
// before first use.
//
// # Lifecycle
//
// NewStore returns a usable *Store or an error — never a half-built store
// (principle 2). Each *Store owns one shard per provisioned partition; every
// shard pins a single *sql.DB connection. Close stops every shard and releases
// every database. Two stores opened over the same local file observe each
// other's committed events (shared-backing parity); an in-memory store is
// private to its single owning *Store.
//
// # Known limitation: remote commit acknowledgement loss
//
// On a remote sqld backend, a COMMIT that succeeds server-side but whose
// acknowledgement is lost in transit surfaces as an error. If that error text
// matches the transient-busy classifier the publish is retried with the same
// pre-generated event ids and reports a spurious we.RevisionConflict; if it
// does not, the raw error is returned. Both outcomes are safe — the batch is
// never written twice and never silently lost — but a caller may observe a
// conflict (or an error) for a batch that actually committed.
package sqlite
