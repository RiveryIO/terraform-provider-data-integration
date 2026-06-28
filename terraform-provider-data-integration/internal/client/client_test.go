package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:   srv.URL,
		Token:     "tok",
		AccountID: "acct1",
		Backoff:   time.Millisecond, // keep retry tests fast
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNew_MissingCredentials(t *testing.T) {
	_, err := New(Config{BaseURL: "https://x", Token: "", AccountID: ""})
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
	for _, want := range []string{"token", "account_id"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestScopedPaths_And_AuthHeader(t *testing.T) {
	var gotPath, gotAuth, gotPlugin string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotPlugin = r.Header.Get("X-Boomi-Plugin")
		w.Write([]byte(`{"_id":"e1","name":"prod"}`))
	}))
	defer srv.Close()

	c := testClient(t, srv)
	if _, err := c.GetEnvironment(context.Background(), "e1"); err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if want := "/v1/accounts/acct1/environments/e1"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q", gotAuth)
	}
	if !contains(gotPlugin, "account=acct1") {
		t.Errorf("plugin header = %q", gotPlugin)
	}
}

func TestEnvScopedPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"cross_id":"r1","name":"flow"}`))
	}))
	defer srv.Close()
	c := testClient(t, srv)
	if _, err := c.GetDataFlow(context.Background(), "env9", "r1"); err != nil {
		t.Fatalf("GetDataFlow: %v", err)
	}
	if want := "/v1/accounts/acct1/environments/env9/rivers/r1"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

func TestNotFoundMapsToErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"nope"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := testClient(t, srv)
	_, err := c.GetDataFlow(context.Background(), "e", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestValidationMapsToErrValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"detail":"bad"}`, http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	c := testClient(t, srv)
	_, err := c.CreateDataFlow(context.Background(), "e", map[string]any{"name": "x"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
}

func TestRetryOn5xxThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"_id":"e1"}`))
	}))
	defer srv.Close()
	c := testClient(t, srv)
	if _, err := c.GetEnvironment(context.Background(), "e1"); err != nil {
		t.Fatalf("GetEnvironment: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
}

func TestRetryExhaustionReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	c := testClient(t, srv)
	_, err := c.GetEnvironment(context.Background(), "e1")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("want APIError 502, got %v", err)
	}
}

func TestListDataFlowsPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			w.Write([]byte(`{"items":[{"river_cross_id":"a"}],"next_page":2}`))
		default:
			w.Write([]byte(`{"items":[{"river_cross_id":"b"}],"next_page":null}`))
		}
	}))
	defer srv.Close()
	c := testClient(t, srv)
	flows, err := c.ListDataFlows(context.Background(), "e")
	if err != nil {
		t.Fatalf("ListDataFlows: %v", err)
	}
	if len(flows) != 2 {
		t.Fatalf("want 2 flows, got %d", len(flows))
	}
	// river_cross_id normalized to id
	if flows[0]["id"] != "a" || flows[1]["id"] != "b" {
		t.Errorf("ids not normalized: %v", flows)
	}
}

func TestUpdateIsReadModifyWriteAndStripsForbidden(t *testing.T) {
	var putBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// server returns forbidden fields + a sub-field the patch doesn't touch
			w.Write([]byte(`{"cross_id":"r1","account_id":"acct1","name":"old","metadata":{"description":"old","keep":"yes"}}`))
		case http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &putBody)
			w.Write([]byte(`{"cross_id":"r1","name":"new"}`))
		}
	}))
	defer srv.Close()
	c := testClient(t, srv)

	_, err := c.UpdateDataFlow(context.Background(), "e", "r1", map[string]any{
		"name":     "new",
		"metadata": map[string]any{"description": "new"},
	})
	if err != nil {
		t.Fatalf("UpdateDataFlow: %v", err)
	}
	// forbidden fields must be stripped from the PUT body
	for _, bad := range []string{"cross_id", "account_id"} {
		if _, present := putBody[bad]; present {
			t.Errorf("forbidden field %q leaked into PUT body", bad)
		}
	}
	// patch wins on description; untouched sub-field is preserved (deep merge)
	meta, _ := putBody["metadata"].(map[string]any)
	if meta["description"] != "new" {
		t.Errorf("description not patched: %v", meta)
	}
	if meta["keep"] != "yes" {
		t.Errorf("deep merge dropped untouched sub-field: %v", meta)
	}
	if putBody["name"] != "new" {
		t.Errorf("name not updated: %v", putBody["name"])
	}
}

func TestDeepMerge(t *testing.T) {
	base := map[string]any{"a": 1, "nested": map[string]any{"x": 1, "y": 2}, "list": []any{1}}
	patch := map[string]any{"nested": map[string]any{"y": 9, "z": 3}, "list": []any{2, 3}}
	got := deepMerge(base, patch)
	want := map[string]any{
		"a":      1,
		"nested": map[string]any{"x": 1, "y": 9, "z": 3},
		"list":   []any{2, 3}, // lists replace wholesale
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deepMerge = %#v, want %#v", got, want)
	}
}

func TestNormalizeIDPrecedence(t *testing.T) {
	cases := []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"id": "keep", "cross_id": "x"}, "keep"},
		{map[string]any{"river_cross_id": "rc"}, "rc"},
		{map[string]any{"cross_id": "c"}, "c"},
		{map[string]any{"_id": "u"}, "u"},
	}
	for _, tc := range cases {
		if got := normalizeID(tc.in)["id"]; got != tc.want {
			t.Errorf("normalizeID(%v) id = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
