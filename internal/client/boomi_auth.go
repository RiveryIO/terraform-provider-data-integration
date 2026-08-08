package client

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// errStaticTokenNotRefreshable signals that a static token cannot be
// refreshed, so a 401 with a static token should surface as-is instead of
// retrying with an unchanged credential.
var errStaticTokenNotRefreshable = errors.New("static token cannot be refreshed")

// TokenSource supplies the bearer token used to authenticate API requests.
// Token returns the current token, fetching it lazily on first use. Refresh
// forces a re-fetch (e.g. after a 401) and returns the new token.
//
// stale is the token that the caller observed fail with a 401. Implementations
// that cache should compare against it: if the cache already holds a
// different (i.e. newer) value than stale, another goroutine has already
// refreshed on this caller's behalf, so Refresh should return that cached
// value instead of exchanging again. This collapses concurrent 401s from
// parallel resource CRUD (Terraform's plugin framework dispatches CRUD
// concurrently within one provider instance) onto a single exchange instead
// of one per failed request.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
	Refresh(ctx context.Context, stale string) (string, error)
}

// staticTokenSource wraps a fixed Data Integration API token — the existing
// auth mode. It never refreshes.
type staticTokenSource struct{ token string }

func newStaticTokenSource(token string) *staticTokenSource {
	return &staticTokenSource{token: token}
}

func (s *staticTokenSource) Token(context.Context) (string, error) { return s.token, nil }

func (s *staticTokenSource) Refresh(context.Context, string) (string, error) {
	return s.token, errStaticTokenNotRefreshable
}

// BoomiJWTSourceConfig configures a BoomiJWTSource.
type BoomiJWTSourceConfig struct {
	// PlatformURL is the Boomi Platform base URL, e.g. https://api.boomi.com.
	PlatformURL string
	// AccountID is the Boomi Platform account ID used in the token exchange
	// URL. This is distinct from the client's Rivery AccountID — the JWT
	// resolves identity, not the Data Integration API's account-scoped URL.
	AccountID string
	Username  string
	APIToken  string

	HTTPClient *http.Client
	MaxRetries int
	Backoff    time.Duration
}

// BoomiJWTSource exchanges a long-lived Boomi Platform API token for a
// short-lived JWT, caches it, and re-exchanges on demand.
//
// Ports rivery_commons/utils/boomi_auth.py (exchange_boomi_token +
// BoomiPlatformAuth) to Go. Safe for concurrent use — the Terraform plugin
// framework may call resource CRUD concurrently across a single provider
// instance, unlike the Python client's effectively single-threaded usage.
type BoomiJWTSource struct {
	cfg BoomiJWTSourceConfig

	mu     sync.Mutex
	cached string
}

// NewBoomiJWTSource builds a BoomiJWTSource, applying defaults and validating
// that the required Boomi credentials are present. The JWT itself is not
// fetched until the first call to Token or Refresh.
func NewBoomiJWTSource(cfg BoomiJWTSourceConfig) (*BoomiJWTSource, error) {
	var missing []string
	if cfg.PlatformURL == "" {
		missing = append(missing, "boomi_platform_url")
	}
	if cfg.AccountID == "" {
		missing = append(missing, "boomi_account_id")
	}
	if cfg.Username == "" {
		missing = append(missing, "boomi_username")
	}
	if cfg.APIToken == "" {
		missing = append(missing, "boomi_api_token")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing Boomi credentials: %s", strings.Join(missing, ", "))
	}

	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultTimeout}
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaultMaxRetries
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = defaultBackoff
	}
	cfg.PlatformURL = strings.TrimRight(cfg.PlatformURL, "/")

	return &BoomiJWTSource{cfg: cfg}, nil
}

// Token returns the cached JWT, exchanging for a new one on first use.
func (s *BoomiJWTSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != "" {
		return s.cached, nil
	}
	return s.exchange(ctx)
}

// Refresh exchanges for a fresh JWT, unless another caller already refreshed
// past the stale value on our behalf while we were waiting for the lock — in
// that case the already-fresh cached value is returned with no extra
// exchange call. Callers always pass the token that just failed with a 401
// (never empty — Token never returns ""), so this only short-circuits when
// the cache has genuinely moved on since the caller last read it.
func (s *BoomiJWTSource) Refresh(ctx context.Context, stale string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != "" && s.cached != stale {
		return s.cached, nil
	}
	return s.exchange(ctx)
}

// exchange calls the Boomi token-generation endpoint and caches the result.
// Retries on transport errors and 429/5xx, mirroring client.request()'s
// retry-with-backoff shape. Callers must hold s.mu.
func (s *BoomiJWTSource) exchange(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/auth/jwt/generate/%s", s.cfg.PlatformURL, s.cfg.AccountID)
	basicCreds := base64.StdEncoding.EncodeToString(
		[]byte(fmt.Sprintf("BOOMI_TOKEN.%s:%s", s.cfg.Username, s.cfg.APIToken)))

	var lastErr error
	for attempt := 0; attempt <= s.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(s.cfg.Backoff * time.Duration(attempt)):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", fmt.Errorf("building Boomi token exchange request: %w", err)
		}
		req.Header.Set("Authorization", "Basic "+basicCreds)
		req.Header.Set("Accept", "*/*")

		resp, err := s.cfg.HTTPClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("Boomi token exchange request error: %w", err)
			continue // transport error — retry
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			jwt := strings.TrimSpace(string(body))
			if jwt == "" {
				return "", errors.New("Boomi token exchange returned an empty JWT")
			}
			s.cached = jwt
			return jwt, nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = &APIError{StatusCode: resp.StatusCode, Message: "Boomi token exchange", Details: truncate(body)}
			continue // 429/5xx — retry
		default:
			return "", &APIError{StatusCode: resp.StatusCode, Message: "Boomi token exchange", Details: truncate(body)}
		}
	}
	return "", lastErr
}
