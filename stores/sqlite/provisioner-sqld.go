package sqlite

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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
}

func newSqldProvisioner(adminURL, dataURL, authToken string) *sqldProvisioner {
	return &sqldProvisioner{
		adminURL:  strings.TrimRight(adminURL, "/"),
		dataURL:   strings.TrimRight(dataURL, "/"),
		authToken: authToken,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *sqldProvisioner) namespace(name PartitionName) string {
	if name.IsDefault() {
		return "default"
	}
	return name.String()
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		return Target{}, fmt.Errorf("sqlite: failed to build namespace request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.authToken)
	}

	resp, err := p.http.Do(req)
	if err != nil {
		return Target{}, fmt.Errorf("sqlite: namespace create failed: %w", err)
	}
	// The body is irrelevant; close it so the connection can be reused.
	_ = resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		// 409 means the namespace already exists, which is success for an
		// idempotent ensure.
		return p.targetFor(namespace), nil
	default:
		return Target{}, fmt.Errorf("sqlite: namespace create returned status %d", resp.StatusCode)
	}
}

func (p *sqldProvisioner) ExistingTarget(_ context.Context, name PartitionName) (Target, bool, error) {
	// sqld has no cheap existence probe distinct from create; the store opens
	// the target lazily and treats a missing namespace as a load miss. Report
	// the addressable target and let the caller's open decide.
	return p.targetFor(p.namespace(name)), true, nil
}

func (p *sqldProvisioner) NamedTargets(_ context.Context) ([]NamedTarget, error) {
	// The sqld admin API used here does not enumerate namespaces; discovery for
	// sqld relies on the store's known set. Returning empty keeps Partitions a
	// union with known.
	return nil, nil
}
