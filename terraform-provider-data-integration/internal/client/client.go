// Package client is a slim Boomi Data Integration (Rivery) public-API client.
//
// It ports the load-bearing patterns from the Python POC (rivery_client.py),
// which in turn ports the BDI plugin's RiveryAPI: bearer-token auth,
// account/environment-scoped paths, read-modify-write edit via deep-merge,
// forbidden-field stripping, list/detail field normalization, and typed
// errors + 5xx retry with backoff.
//
// Customer-facing term is "data flow"; the API/code term is "river" — the API
// paths and fields use river/cross_id. The provider's public surface uses
// data_flow; this client speaks the API's river vocabulary.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTimeout    = 60 * time.Second
	defaultMaxRetries = 3
	defaultBackoff    = time.Second
	userAgent         = "terraform-provider-data-integration/0.1.0"
)

// writeForbiddenFields are stripped before every write — the API rejects them
// as extra_forbidden. Lifted verbatim from the POC / BDI client.
var writeForbiddenFields = map[string]struct{}{
	"title":            {},
	"id":               {},
	"cross_id":         {},
	"_id":              {},
	"account_id":       {},
	"environment_name": {},
	"group_name":       {},
}

// APIError is the base error carrying the HTTP status and server detail.
type APIError struct {
	StatusCode int
	Message    string
	Details    string
}

func (e *APIError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("API error %d: %s (%s)", e.StatusCode, e.Message, e.Details)
	}
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Message)
}

// Sentinel errors for status-code classification (mirrors the POC's typed
// exception hierarchy). Callers use errors.Is to branch — most importantly on
// ErrNotFound, which Read handlers translate into state removal.
var (
	ErrAuth       = errors.New("authentication failed (401/403)")
	ErrNotFound   = errors.New("resource not found (404)")
	ErrValidation = errors.New("validation failed (400/422)")
)

func (e *APIError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAuth
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return ErrValidation
	}
	return nil
}

// Client is an account-scoped Rivery API client. Environment-scoped operations
// take the environmentID explicitly, so a single client serves every
// environment in the account.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	accountID  string
	maxRetries int
	backoff    time.Duration
}

// Config configures a Client. Token, BaseURL and AccountID are required.
type Config struct {
	BaseURL    string
	Token      string
	AccountID  string
	HTTPClient *http.Client
	MaxRetries int
	Backoff    time.Duration
}

// New builds a Client from Config, applying defaults and validating that the
// required credentials are present.
func New(cfg Config) (*Client, error) {
	var missing []string
	if cfg.Token == "" {
		missing = append(missing, "token")
	}
	if cfg.BaseURL == "" {
		missing = append(missing, "api_url")
	}
	if cfg.AccountID == "" {
		missing = append(missing, "account_id")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing credentials: %s", strings.Join(missing, ", "))
	}

	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = defaultMaxRetries
	}
	backoff := cfg.Backoff
	if backoff <= 0 {
		backoff = defaultBackoff
	}

	return &Client{
		httpClient: hc,
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		token:      cfg.Token,
		accountID:  cfg.AccountID,
		maxRetries: retries,
		backoff:    backoff,
	}, nil
}

// AccountID returns the account this client is scoped to.
func (c *Client) AccountID() string { return c.accountID }

func (c *Client) accountPath(suffix string) string {
	return fmt.Sprintf("%s/v1/accounts/%s%s", c.baseURL, c.accountID, suffix)
}

func (c *Client) envPath(environmentID, suffix string) string {
	return fmt.Sprintf("%s/v1/accounts/%s/environments/%s%s", c.baseURL, c.accountID, environmentID, suffix)
}

func (c *Client) headers() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+c.token)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("User-Agent", userAgent)
	// Attribution header — feeds the NR usage dashboard (BDI pattern).
	h.Set("X-Boomi-Plugin", fmt.Sprintf("%s (account=%s)", userAgent, c.accountID))
	return h
}

// request performs an HTTP call against an already-built absolute URL, with
// JSON encode/decode, typed error mapping, and 5xx + transport retry/backoff.
// A nil body sends no payload; out may be nil to discard the response.
func (c *Client) request(ctx context.Context, method, url string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.backoff * time.Duration(attempt)):
			}
		}

		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return fmt.Errorf("building request: %w", err)
		}
		req.Header = c.headers()

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request error: %w", err)
			continue // transport error — retry
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if out == nil || len(respBody) == 0 {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("decoding response: %w", err)
			}
			return nil
		case resp.StatusCode >= 500:
			lastErr = &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s %s", method, url), Details: truncate(respBody)}
			continue // 5xx — retry
		default:
			return &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s %s", method, url), Details: truncate(respBody)}
		}
	}
	return lastErr
}

func truncate(b []byte) string {
	const max = 500
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// deepMerge recursively merges patch into base; patch wins on leaves, and
// lists/scalars replace wholesale (matches BDI/POC semantics where schedulers
// and logic_steps are full-replace, not index-merged).
func deepMerge(base, patch map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		if pv, ok := v.(map[string]any); ok {
			if bv, ok := out[k].(map[string]any); ok {
				out[k] = deepMerge(bv, pv)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func stripForbidden(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if _, bad := writeForbiddenFields[k]; bad {
			continue
		}
		out[k] = v
	}
	return out
}

// normalizeID adds a stable "id" key across list (river_cross_id) vs detail
// (cross_id / _id) response shapes, so the provider always reads one field.
func normalizeID(raw map[string]any) map[string]any {
	if raw == nil {
		return raw
	}
	for _, k := range []string{"id", "river_cross_id", "cross_id", "_id"} {
		if v, ok := raw[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				raw["id"] = s
				return raw
			}
		}
	}
	return raw
}

// ---- Data flows (rivers) ---------------------------------------------------

// ListDataFlows returns every data flow in the environment, handling the
// paginated { items, next_page, ... } envelope.
func (c *Client) ListDataFlows(ctx context.Context, environmentID string) ([]map[string]any, error) {
	var all []map[string]any
	page := 1
	for {
		var resp struct {
			Items    []map[string]any `json:"items"`
			Data     []map[string]any `json:"data"`
			NextPage *int             `json:"next_page"`
		}
		url := c.envPath(environmentID, fmt.Sprintf("/rivers?page=%d", page))
		if err := c.request(ctx, http.MethodGet, url, nil, &resp); err != nil {
			return nil, err
		}
		items := resp.Items
		if items == nil {
			items = resp.Data
		}
		for _, it := range items {
			all = append(all, normalizeID(it))
		}
		if resp.NextPage == nil || *resp.NextPage == 0 || len(items) == 0 {
			break
		}
		page = *resp.NextPage
	}
	return all, nil
}

// GetDataFlow fetches a single data flow by id.
func (c *Client) GetDataFlow(ctx context.Context, environmentID, id string) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/rivers/"+id), nil, &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// CreateDataFlow POSTs a new data flow (forbidden fields stripped) and returns
// the server representation.
func (c *Client) CreateDataFlow(ctx context.Context, environmentID string, body map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/rivers"), stripForbidden(body), &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// UpdateDataFlow performs a read-modify-write: GET current, deep-merge the
// desired patch, strip forbidden fields, PUT the full body. This preserves
// server-managed sub-fields the provider doesn't model.
func (c *Client) UpdateDataFlow(ctx context.Context, environmentID, id string, patch map[string]any) (map[string]any, error) {
	var current map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/rivers/"+id), nil, &current); err != nil {
		return nil, err
	}
	merged := stripForbidden(deepMerge(current, patch))
	var out map[string]any
	if err := c.request(ctx, http.MethodPut, c.envPath(environmentID, "/rivers/"+id), merged, &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// DeleteDataFlow deletes a data flow by id.
func (c *Client) DeleteDataFlow(ctx context.Context, environmentID, id string) error {
	return c.request(ctx, http.MethodDelete, c.envPath(environmentID, "/rivers/"+id), nil, nil)
}

// ---- Connections -----------------------------------------------------------

// GetConnection fetches a connection by id. Per the design note, the API omits
// credentials on read — callers must not overwrite secret state from this.
func (c *Client) GetConnection(ctx context.Context, environmentID, id string) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/connections/"+id), nil, &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// CreateConnection POSTs a new connection.
func (c *Client) CreateConnection(ctx context.Context, environmentID string, body map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/connections"), stripForbidden(body), &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// UpdateConnection PUTs a connection by id (read-modify-write).
func (c *Client) UpdateConnection(ctx context.Context, environmentID, id string, patch map[string]any) (map[string]any, error) {
	var current map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/connections/"+id), nil, &current); err != nil {
		return nil, err
	}
	merged := stripForbidden(deepMerge(current, patch))
	var out map[string]any
	if err := c.request(ctx, http.MethodPut, c.envPath(environmentID, "/connections/"+id), merged, &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// DeleteConnection deletes a connection by id.
func (c *Client) DeleteConnection(ctx context.Context, environmentID, id string) error {
	return c.request(ctx, http.MethodDelete, c.envPath(environmentID, "/connections/"+id), nil, nil)
}

// ---- Environments (account-scoped) -----------------------------------------

// GetEnvironment fetches an environment by id.
func (c *Client) GetEnvironment(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodGet, c.accountPath("/environments/"+id), nil, &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// CreateEnvironment POSTs a new environment.
func (c *Client) CreateEnvironment(ctx context.Context, body map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPost, c.accountPath("/environments"), stripForbidden(body), &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// UpdateEnvironment PUTs an environment by id (read-modify-write).
func (c *Client) UpdateEnvironment(ctx context.Context, id string, patch map[string]any) (map[string]any, error) {
	var current map[string]any
	if err := c.request(ctx, http.MethodGet, c.accountPath("/environments/"+id), nil, &current); err != nil {
		return nil, err
	}
	merged := stripForbidden(deepMerge(current, patch))
	var out map[string]any
	if err := c.request(ctx, http.MethodPut, c.accountPath("/environments/"+id), merged, &out); err != nil {
		return nil, err
	}
	return normalizeID(out), nil
}

// DeleteEnvironment deletes an environment by id.
func (c *Client) DeleteEnvironment(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, c.accountPath("/environments/"+id), nil, nil)
}
