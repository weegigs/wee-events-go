package sqlite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
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

	mu    sync.Mutex
	known map[string]Target
}

func newSqldProvisioner(adminURL, dataURL, authToken string) *sqldProvisioner {
	return &sqldProvisioner{
		adminURL:  strings.TrimRight(adminURL, "/"),
		dataURL:   strings.TrimRight(dataURL, "/"),
		authToken: authToken,
		http:      &http.Client{Timeout: 30 * time.Second},
		known:     map[string]Target{},
	}
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
			target = p.remember(namespace)
			return nil
		case http.StatusBadRequest:
			if strings.Contains(string(body), "already exists") {
				target = p.remember(namespace)
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
	return target, nil
}

func (p *sqldProvisioner) ExistingTarget(_ context.Context, name PartitionName) (Target, bool, error) {
	namespace := p.namespace(name)
	if name.IsDefault() {
		return p.targetFor(namespace), true, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	tgt, ok := p.known[namespace]
	return tgt, ok, nil
}

func (p *sqldProvisioner) NamedTargets(_ context.Context) ([]NamedTarget, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.known))
	for name := range p.known {
		names = append(names, name)
	}
	sort.Strings(names)
	named := make([]NamedTarget, 0, len(names))
	for _, name := range names {
		named = append(named, NamedTarget{Name: name, Target: p.known[name]})
	}
	return named, nil
}

func (p *sqldProvisioner) remember(namespace string) Target {
	tgt := p.targetFor(namespace)
	if namespace == "default" {
		return tgt
	}
	p.mu.Lock()
	p.known[namespace] = tgt
	p.mu.Unlock()
	return tgt
}
