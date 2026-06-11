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

// databaseName builds the platform database name for a partition. The default
// partition uses the bare prefix.
func (p *tursoProvisioner) databaseName(name PartitionName) string {
	if name.IsDefault() {
		return p.config.Prefix
	}
	return p.config.Prefix + "-" + sanitizeTursoName(name.String())
}

func sanitizeTursoName(name string) string {
	// Turso database names allow lowercase alphanumerics and hyphens; map any
	// other byte to a hyphen so arbitrary partition-by names remain valid.
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteByte(c)
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c - 'A' + 'a')
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
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

// NamedTargets lists provisioned databases that belong to this backend and maps
// each platform database name back to its LOGICAL partition name (stripping the
// "<prefix>-" so the named-catalog's wire-name fallback derives the correct
// partition). The bare-prefix database (the default partition) is reported with
// an empty logical name.
func (p *tursoProvisioner) NamedTargets(ctx context.Context) ([]NamedTarget, error) {
	databases, err := p.client.ListDatabases(ctx, p.config.Org)
	if err != nil {
		return nil, err
	}

	prefix := p.config.Prefix + "-"
	var named []NamedTarget
	for _, db := range databases {
		switch {
		case db.Name == p.config.Prefix:
			named = append(named, NamedTarget{Name: "", Target: p.targetFor(db)})
		case strings.HasPrefix(db.Name, prefix):
			logical := strings.TrimPrefix(db.Name, prefix)
			named = append(named, NamedTarget{Name: logical, Target: p.targetFor(db)})
		default:
			// A database that is not part of this backend's prefix namespace.
			continue
		}
	}
	return named, nil
}
