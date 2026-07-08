package sqlite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// sqldProvisioner provisions per-partition namespaces on a single sqld server.
// A namespace is created via the admin API; a 200 or an already-exists 409 is
// success. Databases are reached at the data URL with the namespace as a host
// subdomain, matching sqld's namespace addressing.
type sqldProvisioner struct {
	adminURL  string
	dataURL   string
	authToken string
	http      *http.Client
	// registry is the durable record of provisioned namespaces (the admin API
	// cannot enumerate them); known is a process-local read cache over it.
	registry *sqldRegistry

	mu    sync.Mutex
	known map[string]Target
}

func newSqldProvisioner(adminURL, dataURL, authToken string) *sqldProvisioner {
	p := &sqldProvisioner{
		adminURL:  strings.TrimRight(adminURL, "/"),
		dataURL:   strings.TrimRight(dataURL, "/"),
		authToken: authToken,
		http:      &http.Client{Timeout: 30 * time.Second},
		known:     map[string]Target{},
	}
	p.registry = &sqldRegistry{target: p.targetFor("default")}
	return p
}

// namespace derives the sqld namespace for a partition: a readable fragment
// plus a stable hash of the original name, so distinct partition names that
// sanitize alike (the identity grammar allows '.', '_', '@', '|', ':' and
// mixed case in keys) stay on distinct namespaces.
func (p *sqldProvisioner) namespace(name PartitionName) string {
	if name.IsDefault() {
		return "default"
	}
	const fragmentLimit = 32
	fragment := sanitizeFragment(name.String(), fragmentLimit)
	hash := stableHashHex(name.String())
	if fragment == "" {
		return hash
	}
	return fragment + "-" + hash
}

func (p *sqldProvisioner) targetFor(namespace string) Target {
	return Target{dsn: p.namespaceURL(namespace), authToken: p.authToken}
}

func (p *sqldProvisioner) namespaceURL(namespace string) string {
	// libsql://<namespace>.<host> — split scheme and host of dataURL.
	scheme, host, found := strings.Cut(p.dataURL, "://")
	if !found {
		return p.dataURL
	}
	return fmt.Sprintf("%s://%s.%s", scheme, namespace, host)
}

func (p *sqldProvisioner) EnsureTarget(ctx context.Context, name PartitionName) (Target, error) {
	namespace := p.namespace(name)
	url := fmt.Sprintf("%s/v1/namespaces/%s/create", p.adminURL, namespace)

	var target Target
	err := withRemoteAPIRetry(ctx, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("{}"))
		if err != nil {
			return fmt.Errorf("sqlite: failed to build namespace request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if p.authToken != "" {
			req.Header.Set("Authorization", "Bearer "+p.authToken)
		}

		resp, err := p.http.Do(req)
		if err != nil {
			return retryableRemoteError(fmt.Errorf("sqlite: namespace create failed: %w", err))
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return retryableRemoteError(fmt.Errorf("sqlite: failed to read namespace create response: %w", readErr))
		}

		switch resp.StatusCode {
		case http.StatusOK, http.StatusCreated, http.StatusConflict:
			// 409 means the namespace already exists, which is success for an
			// idempotent ensure.
			return nil
		case http.StatusBadRequest:
			if strings.Contains(string(body), "already exists") {
				return nil
			}
			return fmt.Errorf("sqlite: namespace create returned status %d", resp.StatusCode)
		default:
			if isRemoteAPIRetryableStatus(resp.StatusCode) {
				return retryableRemoteStatusError("namespace create", resp.StatusCode)
			}
			return fmt.Errorf("sqlite: namespace create returned status %d", resp.StatusCode)
		}
	})
	if err != nil {
		return Target{}, err
	}
	target, err = p.remember(ctx, namespace, name)
	if err != nil {
		return Target{}, err
	}
	return target, nil
}

func (p *sqldProvisioner) ExistingTarget(ctx context.Context, name PartitionName) (Target, bool, error) {
	namespace := p.namespace(name)
	if name.IsDefault() {
		return p.targetFor(namespace), true, nil
	}

	p.mu.Lock()
	tgt, ok := p.known[namespace]
	p.mu.Unlock()
	if ok {
		return tgt, true, nil
	}

	// Cache miss: the namespace may have been provisioned by an earlier
	// process, so consult the durable registry before reporting absence.
	_, found, err := p.registry.lookup(ctx, namespace)
	if err != nil || !found {
		return Target{}, false, err
	}
	tgt = p.targetFor(namespace)
	p.mu.Lock()
	p.known[namespace] = tgt
	p.mu.Unlock()
	return tgt, true, nil
}

// NamedTargets lists registered namespaces. Name carries the LOGICAL partition
// name recorded at provisioning time — unlike the platform-derived wire names
// other provisioners report, the registry preserves the original — so the
// named catalog's fallback derives the correct partition even without opening
// the shard.
func (p *sqldProvisioner) NamedTargets(ctx context.Context) ([]NamedTarget, error) {
	entries, err := p.registry.all(ctx)
	if err != nil {
		return nil, err
	}
	named := make([]NamedTarget, 0, len(entries))
	for _, e := range entries {
		named = append(named, NamedTarget{Name: e.partition, Target: p.targetFor(e.namespace)})
	}
	return named, nil
}

// remember durably registers the namespace, then caches its target. The
// registry write is what makes the partition visible to later processes.
func (p *sqldProvisioner) remember(ctx context.Context, namespace string, name PartitionName) (Target, error) {
	tgt := p.targetFor(namespace)
	if namespace == "default" {
		return tgt, nil
	}
	if err := p.registry.record(ctx, namespace, name.String()); err != nil {
		return Target{}, err
	}
	p.mu.Lock()
	p.known[namespace] = tgt
	p.mu.Unlock()
	return tgt, nil
}
