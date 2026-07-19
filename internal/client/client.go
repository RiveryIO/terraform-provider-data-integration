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
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout    = 60 * time.Second
	defaultMaxRetries = 8
	defaultBackoff    = time.Second
	rateLimitBackoff  = 10 * time.Second // fixed pause on 429 before retrying
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
	ErrAuth         = errors.New("authentication failed (401/403)")
	ErrUnauthorized = fmt.Errorf("invalid or expired credentials (401): %w", ErrAuth)
	ErrForbidden    = fmt.Errorf("insufficient permissions (403): %w", ErrAuth)
	ErrNotFound     = errors.New("resource not found (404)")
	ErrValidation   = errors.New("validation failed (400/422)")
)

func (e *APIError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized // wraps ErrAuth, so errors.Is(err, ErrAuth) still holds
	case http.StatusForbidden:
		return ErrForbidden // wraps ErrAuth
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
		_ = resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if out == nil || len(respBody) == 0 {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("decoding response: %w", err)
			}
			return nil
		case resp.StatusCode == http.StatusTooManyRequests:
			// 429 — rate limit hit. Sleep a fixed window then retry.
			lastErr = &APIError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s %s", method, url), Details: truncate(respBody)}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(rateLimitBackoff):
			}
			continue
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

// stripNulls recursively removes nil-valued keys from a map so the PUT body
// does not include explicit nulls that some API validators reject (e.g.
// connection_id/connection_name on built-in connectors).
func stripNulls(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if v == nil {
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = stripNulls(nested)
		} else {
			out[k] = v
		}
	}
	return out
}

// normalizeID adds a stable "id" key across list (river_cross_id) vs detail
// (cross_id / _id) response shapes, so the provider always reads one field.
func normalizeID(raw map[string]any) map[string]any {
	if raw == nil {
		return raw
	}
	// The API exposes id/name/type under resource-specific keys (rivers use
	// cross_id + name + type; connections use connection_name + connection_type_id;
	// environments use environment_name). Normalize them to the canonical keys the
	// resource apply() mappers read, so every resource round-trips cleanly.
	normalizeField(raw, "id", "id", "river_cross_id", "cross_id", "_id")
	normalizeField(raw, "name", "name", "connection_name", "environment_name", "river_name")
	normalizeField(raw, "type", "type", "connection_type", "connection_type_id", "river_type")
	return raw
}

// normalizeField copies the first present, non-empty string value among src keys
// into raw[dest], leaving raw untouched if none are found.
func normalizeField(raw map[string]any, dest string, srcKeys ...string) {
	for _, k := range srcKeys {
		if v, ok := raw[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				raw[dest] = s
				return
			}
		}
	}
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
	merged := stripNulls(stripForbidden(deepMerge(current, patch)))
	// properties is config-authoritative: the plan's properties must replace the
	// server's version entirely, not be merged with it. Deep-merging carries over
	// stale fields (e.g. cdc_settings/cdc_override from a prior CDC config) that
	// make the API reject the update with a 422 validation error.
	if pProps, ok := patch["properties"]; ok {
		merged["properties"] = pProps
	}
	// The API rejects PUT with 400 "can not update properties for an active data
	// flow" if the disable operation hasn't fully settled yet (common for CDC
	// rivers where 204 disable is asynchronous in practice). Retry a few times.
	const maxPUTRetries = 5
	const putRetryDelay = 5 * time.Second
	var out map[string]any
	var lastErr error
	for attempt := range maxPUTRetries {
		lastErr = c.request(ctx, http.MethodPut, c.envPath(environmentID, "/rivers/"+id), merged, &out)
		if lastErr == nil {
			return normalizeID(out), nil
		}
		if attempt < maxPUTRetries-1 && isActiveFlowError(lastErr) {
			time.Sleep(putRetryDelay)
			continue
		}
		break
	}
	return nil, lastErr
}

// isActiveFlowError returns true when the API rejected a PUT because the river
// is still considered active by the backend (happens after a fast 204 disable).
func isActiveFlowError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "can not update properties for an active data flow") ||
		strings.Contains(msg, "Please disable the data flow")
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

// ---- Dataframes (environment-scoped, keyed by name) ------------------------
//
// Unlike rivers/connections, the dataframes API keys resources by their unique
// `name` rather than a cross_id, and the response carries no id field — so the
// resource uses the name as its Terraform id. Only connection_settings is
// mutable on update.

// GetDataFrame fetches a dataframe by name.
func (c *Client) GetDataFrame(ctx context.Context, environmentID, name string) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/dataframes/"+name), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateDataFrame POSTs a new dataframe. body must carry name and (optionally)
// connection_settings.
func (c *Client) CreateDataFrame(ctx context.Context, environmentID string, body map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/dataframes"), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateDataFrame PUTs a dataframe by name. The API only accepts
// connection_settings on update (the name is immutable).
func (c *Client) UpdateDataFrame(ctx context.Context, environmentID, name string, patch map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPut, c.envPath(environmentID, "/dataframes/"+name), patch, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteDataFrame deletes a dataframe by name.
func (c *Client) DeleteDataFrame(ctx context.Context, environmentID, name string) error {
	return c.request(ctx, http.MethodDelete, c.envPath(environmentID, "/dataframes/"+name), nil, nil)
}

// ---- Logicode files (environment-scoped, keyed by file_id) -----------------
//
// Logicode files are Python scripts that back logic-river logicode steps.
// The API is create+read only — DELETE and PUT both return 405. Any change to
// filename or content requires creating a new file (new file_id).
//
// Create flow: POST /logicode_file → {file_id, url (presigned PUT, 24h)}
//              PUT <url> with Content-Type: text/x-python → 200
// Read flow:   GET /logicode_file/{file_id} → {file_id, filename, url (presigned GET, 60s)}
// Delete:      no API endpoint; TF removes from state only (file stays in S3).

// LogicodeFileResponse is the shape returned by POST and GET.
type LogicodeFileResponse struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// CreateLogicodeFile POSTs a new logicode file registration and returns the
// file_id and the presigned S3 PUT URL to upload the script content.
func (c *Client) CreateLogicodeFile(ctx context.Context, environmentID, filename string) (LogicodeFileResponse, error) {
	var out LogicodeFileResponse
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/logicode_file"),
		map[string]any{"file_name": filename}, &out); err != nil {
		return out, err
	}
	return out, nil
}

// UploadLogicodeContent PUTs the Python script content to the presigned S3 URL
// returned by CreateLogicodeFile.
func (c *Client) UploadLogicodeContent(ctx context.Context, uploadURL, content string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL,
		strings.NewReader(content))
	if err != nil {
		return fmt.Errorf("building upload request: %w", err)
	}
	req.Header.Set("Content-Type", "text/x-python")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("uploading logicode content: %w", err)
	}
	defer resp.Body.Close() // nolint:errcheck // best-effort close
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("S3 upload returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

// GetLogicodeFile fetches a logicode file registration by file_id.
func (c *Client) GetLogicodeFile(ctx context.Context, environmentID, fileID string) (LogicodeFileResponse, error) {
	var out LogicodeFileResponse
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/logicode_file/"+fileID), nil, &out); err != nil {
		return out, err
	}
	return out, nil
}

// ---- CDC config (river-scoped CDC offset) ----------------------------------
//
// The CDC offset is the source position a CDC river resumes from (mysql binlog,
// postgres lsn, sqlserver lsn, mongodb resume token, oracle scn). It only exists
// for a CDC-enabled river that has fetched changes; GET 400s until then. The
// body shape is { "config": { "datasource_type": "...", <offset fields> } }.
// Set is a single POST (create == update); there is no PUT.

// GetCDCConfig fetches a river's CDC offset config. Returns ErrValidation (400)
// when the river is CDC but no offset has materialized yet.
func (c *Client) GetCDCConfig(ctx context.Context, environmentID, riverID string) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/rivers/"+riverID+"/cdc_config"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetCDCConfig sets (creates or overwrites) a river's CDC offset. body must be
// the full { "config": {...} } envelope.
func (c *Client) SetCDCConfig(ctx context.Context, environmentID, riverID string, body map[string]any) error {
	return c.request(ctx, http.MethodPost, c.envPath(environmentID, "/rivers/"+riverID+"/cdc_config"), body, nil)
}

// DeleteCDCConfig removes a river's CDC offset.
func (c *Client) DeleteCDCConfig(ctx context.Context, environmentID, riverID string) error {
	return c.request(ctx, http.MethodDelete, c.envPath(environmentID, "/rivers/"+riverID+"/cdc_config"), nil, nil)
}

// ---- Environment variables (environment-scoped key/value collection) -------
//
// Variables are a flat key→value map on the environment, not individually
// addressable resources. The API exposes: GET /variables (whole map),
// PUT /variables (merge — only the keys sent are touched), and
// DELETE /variables?variable_key=<key> (remove one). The provider models each
// key as its own boomi_data_integration_variable resource; merge semantics keep sibling keys
// intact, and Read filters the map for the managed key.

// ListVariables returns the environment's full variable map.
func (c *Client) ListVariables(ctx context.Context, environmentID string) (map[string]any, error) {
	var resp struct {
		Variables map[string]any `json:"variables"`
	}
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/variables"), nil, &resp); err != nil {
		return nil, err
	}
	if resp.Variables == nil {
		resp.Variables = map[string]any{}
	}
	return resp.Variables, nil
}

// PutVariable adds or updates a single variable (merge — other keys untouched).
func (c *Client) PutVariable(ctx context.Context, environmentID, key string, value any) error {
	body := map[string]any{"variables": map[string]any{key: value}}
	return c.request(ctx, http.MethodPut, c.envPath(environmentID, "/variables"), body, nil)
}

// DeleteVariable removes a single variable by key.
func (c *Client) DeleteVariable(ctx context.Context, environmentID, key string) error {
	suffix := "/variables?variable_key=" + url.QueryEscape(key)
	return c.request(ctx, http.MethodDelete, c.envPath(environmentID, suffix), nil, nil)
}

// ---- River variables (river-scoped, replace-all semantics) -----------------
//
// River variables are distinct from environment variables. The API exposes only
// two operations: GET /rivers/{id}/variables (list all) and PUT /rivers/{id}/variables
// (replace-all — variables omitted from the body are deleted). There is no endpoint
// for individual variable CRUD.
//
// Encrypted variable handling: PUT accepts plaintext and the API encrypts it.
// GET returns a stable ciphertext (same value across reads; only changes when a new
// plaintext is PUT). Crucially, PUTting the ciphertext back as-is preserves the value
// without double-encrypting — enabling read-modify-write cycles without decryption.
// There is no decrypt API.

// RiverVariableSettings holds the per-variable metadata flags.
type RiverVariableSettings struct {
	ClearValueOnStart bool `json:"clear_value_on_start"`
	IsMultiValue      bool `json:"is_multi_value"`
	IsEncrypted       bool `json:"is_encrypted"`
}

// RiverVariable is a single item in the river variables collection.
// Value is any because the API returns a string for single/encrypted vars and
// a []any for multi-value vars.
type RiverVariable struct {
	Name     string                `json:"name"`
	Settings RiverVariableSettings `json:"settings"`
	Value    any                   `json:"value"`
}

type riverVariablesPage struct {
	Items []RiverVariable `json:"items"`
}

// ListRiverVariables returns all variables for a river.
func (c *Client) ListRiverVariables(ctx context.Context, environmentID, riverID string) ([]RiverVariable, error) {
	var out riverVariablesPage
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/rivers/"+riverID+"/variables"), nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// PutRiverVariables replaces the full variable list for a river. Pass an empty slice
// to delete all variables.
func (c *Client) PutRiverVariables(ctx context.Context, environmentID, riverID string, items []RiverVariable) ([]RiverVariable, error) {
	body := map[string]any{"items": items}
	var out riverVariablesPage
	if err := c.request(ctx, http.MethodPut, c.envPath(environmentID, "/rivers/"+riverID+"/variables"), body, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ---- Data flow runs (activate + trigger) -----------------------------------
//
// Running a data flow is a two-step imperative action, not a CRUD resource:
// a flow must be active before it can run. ActivateDataFlow enables it (the API
// answers 204 when already active, or 202 with an async operation to poll via
// GetOperation), then RunDataFlow triggers an execution and returns the run id.

// DisableDataFlow disables (deactivates) a data flow. Returns the async
// operation id to poll when the API defers (202); empty means synchronous (204).
func (c *Client) DisableDataFlow(ctx context.Context, environmentID, id string) (string, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/rivers/"+id+"/disable_river"), nil, &out); err != nil {
		return "", err
	}
	if op, ok := out["operation_id"].(string); ok {
		return op, nil
	}
	return "", nil
}

// ActivateDataFlow activates (enables) a data flow. Returns the async operation
// id to poll when the API defers activation (202); an empty id means activation
// completed synchronously (204 / already active).
func (c *Client) ActivateDataFlow(ctx context.Context, environmentID, id string) (string, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/rivers/"+id+"/activate_river"), nil, &out); err != nil {
		return "", err
	}
	if op, ok := out["operation_id"].(string); ok {
		return op, nil
	}
	return "", nil
}

// GetOperation returns an async operation's status ("W" working, "D" done,
// "E" error) and any error message.
func (c *Client) GetOperation(ctx context.Context, environmentID, operationID string) (status, errMsg string, err error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/operations/"+operationID), nil, &out); err != nil {
		return "", "", err
	}
	status, _ = out["status"].(string)
	errMsg, _ = out["error_message"].(string)
	return status, errMsg, nil
}

// EnableCDCDataFlow calls the enable_cdc endpoint which sets ENABLE_LOG=true on
// the river. Must be called after the river is updated to extract_method=log and
// before the first CDC run. Returns the async operation id (empty = synchronous).
func (c *Client) EnableCDCDataFlow(ctx context.Context, environmentID, id string) (string, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/rivers/"+id+"/enable_cdc"), nil, &out); err != nil {
		return "", err
	}
	if op, ok := out["operation_id"].(string); ok {
		return op, nil
	}
	return "", nil
}

// RunDataFlow triggers a run of a data flow, returning the first run id and the
// run group id from the API's { runs: [{run_id,...}], run_group_id } response.
func (c *Client) RunDataFlow(ctx context.Context, environmentID, id string) (runID, runGroupID string, err error) {
	var out struct {
		Runs []struct {
			RunID string `json:"run_id"`
		} `json:"runs"`
		RunGroupID string `json:"run_group_id"`
	}
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/rivers/"+id+"/run"), nil, &out); err != nil {
		return "", "", err
	}
	if len(out.Runs) > 0 {
		runID = out.Runs[0].RunID
	}
	return runID, out.RunGroupID, nil
}

// ---- Connection types (read-only catalog + per-type schema discovery) -------
//
// These are account-agnostic catalog endpoints (not account/environment scoped).
// They let a data source surface the API's own connection-type schema at plan
// time, so new connector types and fields appear without a provider release.

// ListConnectionTypes returns the connection-type catalog. The API paginates
// ({ items, next_page, ... }) and wraps each row in a "fields" object, which
// this unwraps to the inner connection-type record.
func (c *Client) ListConnectionTypes(ctx context.Context) ([]map[string]any, error) {
	return c.listTypeCatalog(ctx, "connections_types", true)
}

// ListSourceTypes returns the source/datasource type catalog
// (/v1/data_source_types) — each row { id, name, connection_type, status,
// section_id, documentation_url, segment, ... }.
func (c *Client) ListSourceTypes(ctx context.Context) ([]map[string]any, error) {
	return c.listTypeCatalog(ctx, "data_source_types", false)
}

// ListTargetTypes returns the target type catalog (/v1/target_types) — each row
// { name, target_type, connection_type, logic_step_type, river_type_id, ... }.
func (c *Client) ListTargetTypes(ctx context.Context) ([]map[string]any, error) {
	return c.listTypeCatalog(ctx, "target_types", false)
}

// listTypeCatalog paginates a top-level /v1/<endpoint> catalog. It pages by
// incrementing index and stops on an empty page or once total_items is reached
// (the list envelope's next_page is a string cursor, not an int, so index-based
// termination is simpler and robust). When unwrapFields is true each row is
// unwrapped from its { "fields": {...} } envelope (connections_types does this;
// data_source_types / target_types return the object directly).
func (c *Client) listTypeCatalog(ctx context.Context, endpoint string, unwrapFields bool) ([]map[string]any, error) {
	var all []map[string]any
	const maxPages = 100 // safety backstop against a misbehaving cursor
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			Items      []map[string]any `json:"items"`
			TotalItems int              `json:"total_items"`
		}
		url := fmt.Sprintf("%s/v1/%s?page=%d", c.baseURL, endpoint, page)
		if err := c.request(ctx, http.MethodGet, url, nil, &resp); err != nil {
			return nil, err
		}
		if len(resp.Items) == 0 {
			break
		}
		for _, it := range resp.Items {
			if unwrapFields {
				if f, ok := it["fields"].(map[string]any); ok {
					all = append(all, f)
					continue
				}
			}
			all = append(all, it)
		}
		if resp.TotalItems > 0 && len(all) >= resp.TotalItems {
			break
		}
	}
	return all, nil
}

// GetConnectionType returns one connection type's property schema —
// { connection_type, connection_type_name, properties: [...] }.
func (c *Client) GetConnectionType(ctx context.Context, connectionType string) (map[string]any, error) {
	var out map[string]any
	url := fmt.Sprintf("%s/v1/connections_types/%s", c.baseURL, connectionType)
	if err := c.request(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
