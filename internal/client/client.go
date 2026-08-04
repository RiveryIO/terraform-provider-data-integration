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
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	// The API exposes id/name/type under resource-specific keys (data flows use
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

// ---- Data flows -------------------------------------------------------------

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
	// data flows where 204 disable is asynchronous in practice). Retry a few times.
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

// isActiveFlowError returns true when the API rejected a PUT because the data
// flow is still considered active by the backend (happens after a fast 204 disable).
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

// UploadConnectionFile uploads a local PEM file to the connection-file endpoint
// and returns the server-side file_path. The file is POSTed as multipart/form-data
// with field name "file". Endpoint:
//
//	POST /v1/accounts/{accountID}/environments/{envID}/connections/{connType}/files
func (c *Client) UploadConnectionFile(ctx context.Context, environmentID, connType, localPath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("opening SSH key file %q: %w", localPath, err)
	}
	defer f.Close() // nolint:errcheck // best-effort close

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(localPath))
	if err != nil {
		return "", fmt.Errorf("creating multipart field: %w", err)
	}
	if _, err = io.Copy(fw, f); err != nil {
		return "", fmt.Errorf("reading SSH key file: %w", err)
	}
	if err = mw.Close(); err != nil {
		return "", fmt.Errorf("finalizing multipart body: %w", err)
	}

	uploadURL := c.envPath(environmentID, fmt.Sprintf("/connections/%s/files", connType))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	if err != nil {
		return "", fmt.Errorf("building upload request: %w", err)
	}
	req.Header = c.headers()
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("uploading SSH key file: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &APIError{StatusCode: resp.StatusCode, Message: "POST " + uploadURL, Details: truncate(respBody)}
	}

	var out struct {
		FilePath string `json:"file_path"`
	}
	if err = json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decoding upload response: %w", err)
	}
	return out.FilePath, nil
}

// UploadConnectionFileContent uploads raw in-memory content (e.g. a value derived
// from an ephemeral resource, which is never written to local disk) to the same
// connection-file endpoint as UploadConnectionFile, and returns the server-side
// file_path. Field name defaults to "file" plus a synthetic name since the API
// only needs bytes, not a real filename.
func (c *Client) UploadConnectionFileContent(ctx context.Context, environmentID, connType, filename, content string) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("creating multipart field: %w", err)
	}
	if _, err = fw.Write([]byte(content)); err != nil {
		return "", fmt.Errorf("writing file content: %w", err)
	}
	if err = mw.Close(); err != nil {
		return "", fmt.Errorf("finalizing multipart body: %w", err)
	}

	uploadURL := c.envPath(environmentID, fmt.Sprintf("/connections/%s/files", connType))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	if err != nil {
		return "", fmt.Errorf("building upload request: %w", err)
	}
	req.Header = c.headers()
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("uploading file content: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &APIError{StatusCode: resp.StatusCode, Message: "POST " + uploadURL, Details: truncate(respBody)}
	}

	var out struct {
		FilePath string `json:"file_path"`
	}
	if err = json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decoding upload response: %w", err)
	}
	return out.FilePath, nil
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
// Unlike data flows/connections, the dataframes API keys resources by their unique
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
// Logicode files are Python scripts that back logic data flow logicode steps.
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

// ---- CDC config (data-flow-scoped CDC offset) ------------------------------
//
// The CDC offset is the source position a CDC data flow resumes from (mysql
// binlog, postgres lsn, sqlserver lsn, mongodb resume token, oracle scn). It
// only exists for a CDC-enabled data flow that has fetched changes; GET 400s
// until then. The body shape is { "config": { "datasource_type": "...",
// <offset fields> } }. Set is a single POST (create == update); there is no PUT.

// GetCDCConfig fetches a data flow's CDC offset config. Returns ErrValidation
// (400) when the data flow is CDC but no offset has materialized yet.
func (c *Client) GetCDCConfig(ctx context.Context, environmentID, dataFlowID string) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/rivers/"+dataFlowID+"/cdc_config"), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetCDCConfig sets (creates or overwrites) a data flow's CDC offset. body
// must be the full { "config": {...} } envelope.
func (c *Client) SetCDCConfig(ctx context.Context, environmentID, dataFlowID string, body map[string]any) error {
	return c.request(ctx, http.MethodPost, c.envPath(environmentID, "/rivers/"+dataFlowID+"/cdc_config"), body, nil)
}

// DeleteCDCConfig removes a data flow's CDC offset.
func (c *Client) DeleteCDCConfig(ctx context.Context, environmentID, dataFlowID string) error {
	return c.request(ctx, http.MethodDelete, c.envPath(environmentID, "/rivers/"+dataFlowID+"/cdc_config"), nil, nil)
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

// ---- Data flow variables (data-flow-scoped, replace-all semantics) ---------
//
// Data flow variables are distinct from environment variables. The API exposes
// only two operations: GET /rivers/{id}/variables (list all) and PUT
// /rivers/{id}/variables (replace-all — variables omitted from the body are
// deleted). There is no endpoint for individual variable CRUD.
//
// Encrypted variable handling: PUT accepts plaintext and the API encrypts it.
// GET returns a stable ciphertext (same value across reads; only changes when a new
// plaintext is PUT). Crucially, PUTting the ciphertext back as-is preserves the value
// without double-encrypting — enabling read-modify-write cycles without decryption.
// There is no decrypt API.

// DataFlowVariableSettings holds the per-variable metadata flags.
type DataFlowVariableSettings struct {
	ClearValueOnStart bool `json:"clear_value_on_start"`
	IsMultiValue      bool `json:"is_multi_value"`
	IsEncrypted       bool `json:"is_encrypted"`
}

// DataFlowVariable is a single item in the data flow variables collection.
// Value is any because the API returns a string for single/encrypted vars and
// a []any for multi-value vars.
type DataFlowVariable struct {
	Name     string                   `json:"name"`
	Settings DataFlowVariableSettings `json:"settings"`
	Value    any                      `json:"value"`
}

type dataFlowVariablesPage struct {
	Items []DataFlowVariable `json:"items"`
}

// ListDataFlowVariables returns all variables for a data flow.
func (c *Client) ListDataFlowVariables(ctx context.Context, environmentID, dataFlowID string) ([]DataFlowVariable, error) {
	var out dataFlowVariablesPage
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/rivers/"+dataFlowID+"/variables"), nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// PutDataFlowVariables replaces the full variable list for a data flow. Pass
// an empty slice to delete all variables.
func (c *Client) PutDataFlowVariables(ctx context.Context, environmentID, dataFlowID string, items []DataFlowVariable) ([]DataFlowVariable, error) {
	body := map[string]any{"items": items}
	var out dataFlowVariablesPage
	if err := c.request(ctx, http.MethodPut, c.envPath(environmentID, "/rivers/"+dataFlowID+"/variables"), body, &out); err != nil {
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

// ---- Connection test (pull-request driven) ---------------------------------
//
// The API has no dedicated "test connection" route. The console tests a
// connection by asking the worker fleet (rivery_back) to actually connect to
// the source/target and read its metadata — a "pull request". A get_db_metadata
// (or get_schemas / get_databases) pull request opens a real connection, so a
// Done result proves reachability + credentials and an Error result carries the
// real connector error (e.g. ORA-12547 when a TLS-only Autonomous DB rejects a
// plaintext connect). This is exactly what the connection-test data source uses.

// ConnectionTestResult is the terminal outcome of a connection-test pull request.
type ConnectionTestResult struct {
	OperationID  string
	RunID        string
	Status       string // "D" done/reachable, "E" error, or the last polled state
	ErrorMessage string
}

// TestConnection creates a pull request (POST /pull_requests) and polls the
// resulting operation to a terminal state. body is the BasePullRequestSchema
// object (task_type / datasource_id / task / pull_request_inputs). Poll status
// values are "W" (working), "R" (running), "D" (done), "E" (error).
func (c *Client) TestConnection(ctx context.Context, environmentID string, body map[string]any, pollInterval, timeout time.Duration) (ConnectionTestResult, error) {
	var created struct {
		OperationID  string  `json:"operation_id"`
		RunID        string  `json:"run_id"`
		Status       string  `json:"status"`
		ErrorMessage *string `json:"error_message"`
	}
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/pull_requests"), body, &created); err != nil {
		return ConnectionTestResult{}, err
	}
	res := ConnectionTestResult{OperationID: created.OperationID, RunID: created.RunID, Status: created.Status}
	if created.ErrorMessage != nil {
		res.ErrorMessage = *created.ErrorMessage
	}
	if res.OperationID == "" {
		return res, fmt.Errorf("pull request did not return an operation_id")
	}

	deadline := time.Now().Add(timeout)
	for res.Status == "W" || res.Status == "R" || res.Status == "" {
		if time.Now().After(deadline) {
			return res, fmt.Errorf("connection test timed out after %s (operation %s still %q)", timeout, res.OperationID, res.Status)
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(pollInterval):
		}
		status, errMsg, err := c.GetOperation(ctx, environmentID, res.OperationID)
		if err != nil {
			return res, err
		}
		res.Status = status
		if errMsg != "" {
			res.ErrorMessage = errMsg
		}
	}
	return res, nil
}

// ---- Source metadata discovery (get_db_metadata pull request) --------------
//
// The console's mapping tab discovers a source's schema/columns by asking the
// worker fleet to introspect the connection — the same get_db_metadata "pull
// request" the connection test uses, but here we keep the operation's result
// payload. The result is the nested {schema:{table:{columns:[...]}}} shape the
// discover_schema.py helper parses. RDBMS sources only; API/SaaS connectors
// route metadata differently (TODO).

// SourceMetadataResult is the terminal outcome of a get_db_metadata pull request,
// carrying the discovered metadata payload on success.
type SourceMetadataResult struct {
	OperationID  string
	RunID        string
	Status       string // "D" done, "E" error, or the last polled state
	ErrorMessage string
	// Result is the raw operation.result — the nested
	// {schema:{table:{columns:[...]}}} discovery payload (nil unless Status=="D").
	Result map[string]any
}

// DiscoverSourceMetadata creates a get_db_metadata pull request (POST
// /pull_requests) and polls the resulting operation to a terminal state,
// returning the operation's result payload on success. body is the
// BasePullRequestSchema object (task_type / datasource_id / task:"get_db_metadata"
// / pull_request_inputs). Poll status values are "W" (working), "R" (running),
// "D" (done), "E" (error).
func (c *Client) DiscoverSourceMetadata(ctx context.Context, environmentID string, body map[string]any, pollInterval, timeout time.Duration) (SourceMetadataResult, error) {
	var created struct {
		OperationID  string  `json:"operation_id"`
		RunID        string  `json:"run_id"`
		Status       string  `json:"status"`
		ErrorMessage *string `json:"error_message"`
	}
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/pull_requests"), body, &created); err != nil {
		return SourceMetadataResult{}, err
	}
	res := SourceMetadataResult{OperationID: created.OperationID, RunID: created.RunID, Status: created.Status}
	if created.ErrorMessage != nil {
		res.ErrorMessage = *created.ErrorMessage
	}
	if res.OperationID == "" {
		return res, fmt.Errorf("pull request did not return an operation_id")
	}

	deadline := time.Now().Add(timeout)
	for res.Status == "W" || res.Status == "R" || res.Status == "" {
		if time.Now().After(deadline) {
			return res, fmt.Errorf("source metadata discovery timed out after %s (operation %s still %q)", timeout, res.OperationID, res.Status)
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(pollInterval):
		}
		var op map[string]any
		if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/operations/"+res.OperationID), nil, &op); err != nil {
			return res, err
		}
		res.Status, _ = op["status"].(string)
		if msg, _ := op["error_message"].(string); msg != "" {
			res.ErrorMessage = msg
		}
		if res.Status == "D" {
			if r, ok := op["result"].(map[string]any); ok {
				res.Result = r
			}
		}
	}
	return res, nil
}

// ---- Target metadata discovery (get_databases/get_datasets/get_catalogs) ----
//
// The console's target-mapping picker lists a warehouse's top-level containers
// (Snowflake databases, BigQuery datasets, Databricks catalogs) by asking the
// worker fleet to introspect the TARGET connection — the same "pull request"
// mechanic as source discovery, but with task_type:"target" and a per-warehouse
// task verb. Unlike source get_db_metadata (a nested object), the target result
// is typically a flat JSON array (e.g. snowflake get_databases ->
// ["ANALYTICS","RAW_DATA",...]); bigquery/databricks may return an array of
// objects, so the result is kept as the raw decoded value.

// TargetMetadataResult is the terminal outcome of a target-metadata pull request,
// carrying the discovered listing payload on success.
type TargetMetadataResult struct {
	OperationID  string
	RunID        string
	Status       string // "D" done, "E" error, or the last polled state
	ErrorMessage string
	// Result is the raw operation.result — for target discovery usually a
	// []any of database/dataset/catalog names (nil unless Status=="D").
	Result any
}

// DiscoverTargetMetadata creates a target-metadata pull request (POST
// /pull_requests) and polls the resulting operation to a terminal state,
// returning the operation's result payload on success. body is the
// BasePullRequestSchema object (task_type:"target" / datasource_id / task
// (get_databases|get_datasets|get_catalogs) / pull_request_inputs). Poll status
// values are "W" (working), "R" (running), "D" (done), "E" (error).
func (c *Client) DiscoverTargetMetadata(ctx context.Context, environmentID string, body map[string]any, pollInterval, timeout time.Duration) (TargetMetadataResult, error) {
	var created struct {
		OperationID  string  `json:"operation_id"`
		RunID        string  `json:"run_id"`
		Status       string  `json:"status"`
		ErrorMessage *string `json:"error_message"`
	}
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/pull_requests"), body, &created); err != nil {
		return TargetMetadataResult{}, err
	}
	res := TargetMetadataResult{OperationID: created.OperationID, RunID: created.RunID, Status: created.Status}
	if created.ErrorMessage != nil {
		res.ErrorMessage = *created.ErrorMessage
	}
	if res.OperationID == "" {
		return res, fmt.Errorf("pull request did not return an operation_id")
	}

	deadline := time.Now().Add(timeout)
	for res.Status == "W" || res.Status == "R" || res.Status == "" {
		if time.Now().After(deadline) {
			return res, fmt.Errorf("target metadata discovery timed out after %s (operation %s still %q)", timeout, res.OperationID, res.Status)
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(pollInterval):
		}
		var op map[string]any
		if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/operations/"+res.OperationID), nil, &op); err != nil {
			return res, err
		}
		res.Status, _ = op["status"].(string)
		if msg, _ := op["error_message"].(string); msg != "" {
			res.ErrorMessage = msg
		}
		if res.Status == "D" {
			res.Result = op["result"]
		}
	}
	return res, nil
}

// EnableCDCDataFlow calls the CDC-enable endpoint which sets ENABLE_LOG=true on
// the data flow. Must be called after the data flow is updated to extract_method=log
// and before the first CDC run. Returns the async operation id (empty = synchronous).
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

// ---- Data flow groups -------------------------------------------------------

// DataFlowGroup is the normalised view of a data flow group returned by the v1 API.
type DataFlowGroup struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Icon      string `json:"icon"`
	IsDefault bool   `json:"is_default"`
}

// ListDataFlowGroups returns every data flow group in the environment. The v1
// API exposes GET only for groups; creation/mutation is UI-only
// (allow_from_api=false on the internal route). This method pages until exhausted.
func (c *Client) ListDataFlowGroups(ctx context.Context, environmentID string) ([]DataFlowGroup, error) {
	var all []DataFlowGroup
	for page := 1; ; page++ {
		var resp struct {
			Items      []map[string]any `json:"items"`
			TotalItems int              `json:"total_items"`
		}
		u := c.envPath(environmentID, fmt.Sprintf("/river_groups?page=%d", page))
		if err := c.request(ctx, http.MethodGet, u, nil, &resp); err != nil {
			return nil, err
		}
		for _, it := range resp.Items {
			str := func(v any) string {
				s, _ := v.(string)
				return s
			}
			id := str(it["cross_id"])
			if id == "" {
				id = str(it["id"])
			}
			all = append(all, DataFlowGroup{
				ID:        id,
				Name:      str(it["name"]),
				Color:     str(it["color"]),
				Icon:      str(it["icon"]),
				IsDefault: it["is_default"] == true,
			})
		}
		if len(resp.Items) == 0 || (resp.TotalItems > 0 && len(all) >= resp.TotalItems) {
			break
		}
	}
	return all, nil
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

// ---- Blueprints (environment-scoped) ---------------------------------------
//
// Customer-facing term is "blueprint"; the API/code term is "recipe" — same
// river/data_flow-style split documented at the top of this file. A blueprint
// is two API entities: the recipe_file (the uploaded YAML content, keyed by
// cross_id) and the recipe (the named entity referencing a recipe_file's
// cross_id). Both support full CRUD, unlike the create+read-only logicode
// file API.
//
// recipe_files flow: POST/PUT multipart {file} → {cross_id, filename, ...}
//                     GET/{cross_id} → same shape (content itself is never
//                     read back — the API returns a short-lived presigned S3
//                     URL, not the YAML text, so content is config-authoritative
//                     like logicode).
// recipe flow:        POST/PUT JSON {name, file_cross_id, description} →
//                     {cross_id, name, file_cross_id, description, ...}

// BlueprintFileResponse is the shape returned by recipe_files create/update/get.
type BlueprintFileResponse struct {
	CrossID  string `json:"cross_id"`
	Filename string `json:"filename"`
}

// blueprintFileMultipart builds and sends the multipart request shared by
// CreateBlueprintFile and UpdateBlueprintFile — both POST/PUT a single "file"
// form field and decode the same response shape.
func (c *Client) blueprintFileMultipart(
	ctx context.Context, method, url, filename, content string,
) (BlueprintFileResponse, error) {
	var out BlueprintFileResponse

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return out, fmt.Errorf("creating multipart field: %w", err)
	}
	if _, err = fw.Write([]byte(content)); err != nil {
		return out, fmt.Errorf("writing blueprint content: %w", err)
	}
	if err = mw.Close(); err != nil {
		return out, fmt.Errorf("finalizing multipart body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, &buf)
	if err != nil {
		return out, fmt.Errorf("building request: %w", err)
	}
	req.Header = c.headers()
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("uploading blueprint content: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &APIError{StatusCode: resp.StatusCode, Message: method + " " + url, Details: truncate(respBody)}
	}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &out); err != nil {
			return out, fmt.Errorf("decoding response: %w", err)
		}
	}
	return out, nil
}

// CreateBlueprintFile uploads new blueprint YAML content and returns the
// assigned file cross_id.
func (c *Client) CreateBlueprintFile(ctx context.Context, environmentID, filename, content string) (BlueprintFileResponse, error) {
	url := c.envPath(environmentID, "/recipes/files")
	return c.blueprintFileMultipart(ctx, http.MethodPost, url, filename, content)
}

// UpdateBlueprintFile replaces the content of an existing blueprint file
// in-place, keeping its cross_id stable.
func (c *Client) UpdateBlueprintFile(ctx context.Context, environmentID, crossID, filename, content string) (BlueprintFileResponse, error) {
	url := c.envPath(environmentID, "/recipes/files/"+crossID)
	return c.blueprintFileMultipart(ctx, http.MethodPut, url, filename, content)
}

// GetBlueprintFile fetches a blueprint file's metadata by cross_id. Content
// is never returned (only a short-lived presigned URL the API doesn't expose
// here), so this is used purely for drift (existence) detection.
func (c *Client) GetBlueprintFile(ctx context.Context, environmentID, crossID string) (BlueprintFileResponse, error) {
	var out BlueprintFileResponse
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/recipes/files/"+crossID), nil, &out); err != nil {
		return out, err
	}
	return out, nil
}

// DeleteBlueprintFile deletes a blueprint file by cross_id.
func (c *Client) DeleteBlueprintFile(ctx context.Context, environmentID, crossID string) error {
	return c.request(ctx, http.MethodDelete, c.envPath(environmentID, "/recipes/files/"+crossID), nil, nil)
}

// GetBlueprint fetches a blueprint (recipe) by cross_id.
func (c *Client) GetBlueprint(ctx context.Context, environmentID, crossID string) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodGet, c.envPath(environmentID, "/recipes/"+crossID), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateBlueprint creates a blueprint (recipe) referencing an already-uploaded
// blueprint file's cross_id. body: {name, file_cross_id, description?}.
func (c *Client) CreateBlueprint(ctx context.Context, environmentID string, body map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPost, c.envPath(environmentID, "/recipes"), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateBlueprint updates a blueprint's name, file_cross_id, and/or description.
func (c *Client) UpdateBlueprint(ctx context.Context, environmentID, crossID string, patch map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := c.request(ctx, http.MethodPut, c.envPath(environmentID, "/recipes/"+crossID), patch, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteBlueprint deletes a blueprint (recipe) by cross_id. Does not delete
// the underlying blueprint file — that is a separate resource with its own
// lifecycle.
func (c *Client) DeleteBlueprint(ctx context.Context, environmentID, crossID string) error {
	return c.request(ctx, http.MethodDelete, c.envPath(environmentID, "/recipes/"+crossID), nil, nil)
}
