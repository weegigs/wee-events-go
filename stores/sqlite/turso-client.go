package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// tursoDatabase is the platform API's database record (subset used here).
type tursoDatabase struct {
	Name     string `json:"Name"`
	Hostname string `json:"Hostname"`
}

// tursoClient is the Turso Platform API surface the provisioner needs. The real
// implementation calls the HTTP API; tests use an in-memory fake.
type tursoClient interface {
	CreateDatabase(ctx context.Context, org, group, name string) (tursoDatabase, bool, error) // bool: already existed
	GetDatabase(ctx context.Context, org, name string) (tursoDatabase, bool, error)
	ListDatabases(ctx context.Context, org string) ([]tursoDatabase, error)
	DeleteDatabase(ctx context.Context, org, name string) error
}

type httpTursoClient struct {
	apiToken string
	baseURL  string
	http     *http.Client
}

func newHTTPTursoClient(apiToken string) *httpTursoClient {
	return &httpTursoClient{
		apiToken: apiToken,
		baseURL:  "https://api.turso.tech/v1",
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *httpTursoClient) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("sqlite: failed to encode turso request: %w", err)
		}
	}

	var status int
	err := withRemoteAPIRetry(ctx, func() error {
		reader := bytes.NewReader(encoded)
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
		if err != nil {
			return fmt.Errorf("sqlite: failed to build turso request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			return retryableRemoteError(fmt.Errorf("sqlite: turso request failed: %w", err))
		}
		defer func() { _ = resp.Body.Close() }()
		status = resp.StatusCode

		if isRemoteAPIRetryableStatus(resp.StatusCode) {
			_, _ = io.Copy(io.Discard, resp.Body)
			return retryableRemoteStatusError("turso request", resp.StatusCode)
		}

		if out != nil && resp.StatusCode/100 == 2 {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("sqlite: failed to decode turso response: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return status, err
	}
	return status, nil
}

func (c *httpTursoClient) CreateDatabase(ctx context.Context, org, group, name string) (tursoDatabase, bool, error) {
	var out struct {
		Database tursoDatabase `json:"database"`
	}
	status, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/organizations/%s/databases", org),
		map[string]string{"name": name, "group": group}, &out)
	if err != nil {
		return tursoDatabase{}, false, err
	}
	switch status {
	case http.StatusOK, http.StatusCreated:
		return out.Database, false, nil
	case http.StatusConflict:
		db, ok, err := c.GetDatabase(ctx, org, name)
		return db, ok, err
	default:
		return tursoDatabase{}, false, fmt.Errorf("sqlite: turso create returned status %d", status)
	}
}

func (c *httpTursoClient) GetDatabase(ctx context.Context, org, name string) (tursoDatabase, bool, error) {
	var out struct {
		Database tursoDatabase `json:"database"`
	}
	status, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/organizations/%s/databases/%s", org, name), nil, &out)
	if err != nil {
		return tursoDatabase{}, false, err
	}
	if status == http.StatusNotFound {
		return tursoDatabase{}, false, nil
	}
	if status/100 != 2 {
		return tursoDatabase{}, false, fmt.Errorf("sqlite: turso get returned status %d", status)
	}
	return out.Database, true, nil
}

func (c *httpTursoClient) ListDatabases(ctx context.Context, org string) ([]tursoDatabase, error) {
	var out struct {
		Databases []tursoDatabase `json:"databases"`
	}
	status, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/organizations/%s/databases", org), nil, &out)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, fmt.Errorf("sqlite: turso list returned status %d", status)
	}
	return out.Databases, nil
}

func (c *httpTursoClient) DeleteDatabase(ctx context.Context, org, name string) error {
	status, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/organizations/%s/databases/%s", org, name), nil, nil)
	if err != nil {
		return err
	}
	if status/100 != 2 && status != http.StatusNotFound {
		return fmt.Errorf("sqlite: turso delete returned status %d", status)
	}
	return nil
}
