package sqlite

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// TursoConfig configures the Turso platform backend. Org/Group/Prefix/APIToken
// drive provisioning; GroupToken is the database access token written into each
// shard's Target.
type TursoConfig struct {
	Org        string
	Group      string
	Prefix     string
	APIToken   string
	GroupToken string
}

// tursoProvisioner maps partitions to per-partition Turso databases named
// "<prefix>-<sanitized>". It caches name->target and tolerates the create
// already-exists race by re-fetching.
type tursoProvisioner struct {
	client tursoClient
	config TursoConfig

	mu    sync.Mutex
	cache map[string]Target
}

func newTursoProvisioner(client tursoClient, config TursoConfig) *tursoProvisioner {
	return &tursoProvisioner{client: client, config: config, cache: map[string]Target{}}
}

// databaseName builds the platform database name for a partition using the
// wee-events.rs scheme: the default partition is the sanitized prefix alone;
// named partitions carry a readable fragment plus a stable hash (see
// remote-name.go).
func (p *tursoProvisioner) databaseName(name PartitionName) string {
	if name.IsDefault() {
		return sanitizeDatabaseName("", p.config.Prefix)
	}
	return sanitizeDatabaseName(name.String(), p.config.Prefix)
}

func (p *tursoProvisioner) targetFor(db tursoDatabase) Target {
	return Target{dsn: "libsql://" + db.Hostname, authToken: p.config.GroupToken}
}

func (p *tursoProvisioner) EnsureTarget(ctx context.Context, name PartitionName) (Target, error) {
	dbName := p.databaseName(name)

	p.mu.Lock()
	if tgt, ok := p.cache[dbName]; ok {
		p.mu.Unlock()
		return tgt, nil
	}
	p.mu.Unlock()

	db, _, err := p.client.CreateDatabase(ctx, p.config.Org, p.config.Group, dbName)
	if err != nil {
		return Target{}, fmt.Errorf("sqlite: failed to provision turso database %q: %w", dbName, err)
	}
	tgt := p.targetFor(db)

	p.mu.Lock()
	p.cache[dbName] = tgt
	p.mu.Unlock()
	return tgt, nil
}

func (p *tursoProvisioner) ExistingTarget(ctx context.Context, name PartitionName) (Target, bool, error) {
	dbName := p.databaseName(name)

	p.mu.Lock()
	if tgt, ok := p.cache[dbName]; ok {
		p.mu.Unlock()
		return tgt, true, nil
	}
	p.mu.Unlock()

	db, ok, err := p.client.GetDatabase(ctx, p.config.Org, dbName)
	if err != nil || !ok {
		return Target{}, false, err
	}
	tgt := p.targetFor(db)

	p.mu.Lock()
	p.cache[dbName] = tgt
	p.mu.Unlock()
	return tgt, true, nil
}

// NamedTargets lists provisioned databases that belong to this backend. The
// reported Name is the WIRE name (the fragment-hash remainder after the
// managed prefix); the hash makes the platform name lossy, so the true
// logical partition name is recovered from each shard's partition metadata
// (written by PrepareShard). The wire name only serves the named-catalog's
// legacy fallback. The default-partition database is reported with an empty
// name.
func (p *tursoProvisioner) NamedTargets(ctx context.Context) ([]NamedTarget, error) {
	databases, err := p.client.ListDatabases(ctx, p.config.Org)
	if err != nil {
		return nil, err
	}

	defaultName := sanitizeDatabaseName("", p.config.Prefix)
	namedPrefix := namedDatabasePrefix(p.config.Prefix) + "-"
	var named []NamedTarget
	for _, db := range databases {
		switch {
		case db.Name == defaultName:
			named = append(named, NamedTarget{Name: "", Target: p.targetFor(db)})
		case strings.HasPrefix(db.Name, namedPrefix):
			wire := strings.TrimPrefix(db.Name, namedPrefix)
			named = append(named, NamedTarget{Name: wire, Target: p.targetFor(db)})
		default:
			// A database that is not part of this backend's managed namespace.
			continue
		}
	}
	return named, nil
}
