package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testBoomiSource(t *testing.T, srv *httptest.Server) *BoomiJWTSource {
	t.Helper()
	s, err := NewBoomiJWTSource(BoomiJWTSourceConfig{
		PlatformURL: srv.URL,
		AccountID:   "boomi-acct",
		Username:    "user@example.com",
		APIToken:    "api-tok",
		Backoff:     time.Millisecond, // keep retry tests fast
	})
	if err != nil {
		t.Fatalf("NewBoomiJWTSource: %v", err)
	}
	return s
}

func TestNewBoomiJWTSource_MissingCredentials(t *testing.T) {
	_, err := NewBoomiJWTSource(BoomiJWTSourceConfig{})
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
	for _, want := range []string{"boomi_platform_url", "boomi_account_id", "boomi_username", "boomi_api_token"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestBoomiJWTSource_Exchange(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte("  jwt-1  \n")) // exercise TrimSpace
	}))
	defer srv.Close()

	s := testBoomiSource(t, srv)
	tok, err := s.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "jwt-1" {
		t.Errorf("token = %q, want %q", tok, "jwt-1")
	}
	if want := "/auth/jwt/generate/boomi-acct"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	// base64("BOOMI_TOKEN.user@example.com:api-tok")
	if want := "Basic Qk9PTUlfVE9LRU4udXNlckBleGFtcGxlLmNvbTphcGktdG9r"; gotAuth != want {
		t.Errorf("auth = %q, want %q", gotAuth, want)
	}
	if gotAccept != "*/*" {
		t.Errorf("accept = %q, want */*", gotAccept)
	}
}

func TestBoomiJWTSource_CacheHit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("jwt-cached"))
	}))
	defer srv.Close()

	s := testBoomiSource(t, srv)
	for i := 0; i < 3; i++ {
		tok, err := s.Token(context.Background())
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if tok != "jwt-cached" {
			t.Errorf("token = %q", tok)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("exchange calls = %d, want 1 (Token should reuse the cache)", got)
	}
}

func TestBoomiJWTSource_Refresh_ExchangesWhenStaleMatchesCache(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_, _ = w.Write([]byte("jwt-" + string(rune('0'+n))))
	}))
	defer srv.Close()

	s := testBoomiSource(t, srv)
	first, err := s.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	// stale == first == whatever's currently cached, so nobody's refreshed
	// ahead of us — Refresh should genuinely re-exchange.
	second, err := s.Refresh(context.Background(), first)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if first == second {
		t.Errorf("Refresh should re-exchange, got same token %q twice", first)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("exchange calls = %d, want 2", got)
	}
}

func TestBoomiJWTSource_Refresh_SkipsExchangeIfAlreadySuperseded(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte("jwt-current"))
	}))
	defer srv.Close()

	s := testBoomiSource(t, srv)
	current, err := s.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	// Refresh is called with a stale value that does NOT match what's
	// currently cached — as if this caller's request failed on an even
	// older token, and someone else already refreshed past it in the
	// meantime. Should return the current cache without exchanging again.
	got, err := s.Refresh(context.Background(), "some-older-token-not-in-cache")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got != current {
		t.Errorf("Refresh() = %q, want the already-cached %q", got, current)
	}
	if calls.Load() != 1 {
		t.Errorf("exchange calls = %d, want 1 (Refresh should not have re-exchanged)", calls.Load())
	}
}

func TestBoomiJWTSource_Refresh_ConcurrentCallersCollapseToOneExchange(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 {
			// The priming Token() call below is exchange #1 and must return
			// immediately. Only the first of the two concurrent Refresh calls
			// (necessarily #2, since the mutex fully serializes exchange())
			// gets held open, giving the second caller time to queue up
			// on the lock before #1 completes and updates the cache.
			<-release
		}
		_, _ = w.Write([]byte("jwt-" + string(rune('0'+n))))
	}))
	defer srv.Close()

	s := testBoomiSource(t, srv)
	stale, err := s.Token(context.Background()) // exchange #1, returns immediately
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	baseline := calls.Load()
	results := make(chan string, 2)
	for i := 0; i < 2; i++ {
		go func() {
			tok, err := s.Refresh(context.Background(), stale)
			if err != nil {
				t.Errorf("Refresh: %v", err)
			}
			results <- tok
		}()
	}
	time.Sleep(20 * time.Millisecond) // let both goroutines queue up on the mutex
	close(release)

	first, second := <-results, <-results
	if first != second {
		t.Errorf("concurrent Refresh callers got different tokens: %q vs %q", first, second)
	}
	if got := calls.Load() - baseline; got != 1 {
		t.Errorf("exchange calls during the concurrent Refresh phase = %d, want exactly 1 "+
			"(second caller should reuse the first's result instead of exchanging again)", got)
	}
}

func TestBoomiJWTSource_RetriesOn429And5xxThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			_, _ = w.Write([]byte("jwt-ok"))
		}
	}))
	defer srv.Close()

	s := testBoomiSource(t, srv)
	tok, err := s.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "jwt-ok" {
		t.Errorf("token = %q, want jwt-ok", tok)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("exchange calls = %d, want 3", got)
	}
}

func TestBoomiJWTSource_NonRetryableStatusFailsFast(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	s := testBoomiSource(t, srv)
	if _, err := s.Token(context.Background()); err == nil {
		t.Fatal("expected error for 401 from the exchange endpoint")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("exchange calls = %d, want 1 (non-retryable status should not retry)", got)
	}
}

func TestClient_RefreshesOnceOn401(t *testing.T) {
	var refreshes, requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") == "Bearer fresh" {
			_, _ = w.Write([]byte(`{"_id":"e1","name":"prod"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	fake := &fakeTokenSource{token: "stale", refreshed: "fresh", onRefresh: func() { refreshes.Add(1) }}
	c, err := New(Config{BaseURL: srv.URL, TokenSource: fake, AccountID: "acct1", Backoff: time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.GetEnvironment(context.Background(), "e1"); err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if got := refreshes.Load(); got != 1 {
		t.Errorf("refreshes = %d, want exactly 1", got)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want exactly 2 (original + one retry)", got)
	}
}

func TestClient_PersistentAuthFailure_DoesNotLoopForever(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	fake := &fakeTokenSource{token: "stale", refreshed: "still-stale"}
	c, err := New(Config{BaseURL: srv.URL, TokenSource: fake, AccountID: "acct1", Backoff: time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.GetEnvironment(context.Background(), "e1")
	if err == nil {
		t.Fatal("expected persistent 401 to surface as an error")
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want exactly 2 (original + one retry, then give up)", got)
	}
}

func TestClient_StaticToken_401IsNotRetried(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := testClient(t, srv)
	_, err := c.GetEnvironment(context.Background(), "e1")
	if err == nil {
		t.Fatal("expected 401 to surface as an error")
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want exactly 1 (static token can't be refreshed, no point retrying)", got)
	}
}

// fakeTokenSource is a minimal TokenSource test double: Token returns the
// initial value once, Refresh swaps in the refreshed value (and never
// errors), so tests can assert the client's 401-retry-once behavior without
// depending on BoomiJWTSource / a second HTTP server.
type fakeTokenSource struct {
	token, refreshed string
	onRefresh        func()
}

func (f *fakeTokenSource) Token(context.Context) (string, error) { return f.token, nil }

func (f *fakeTokenSource) Refresh(context.Context, string) (string, error) {
	f.token = f.refreshed
	if f.onRefresh != nil {
		f.onRefresh()
	}
	return f.token, nil
}
