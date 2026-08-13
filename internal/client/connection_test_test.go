package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestTestConnection_TimeoutReturnsErrConnectionTestTimeoutAndPartialResult
// pins the behaviour TestConnection's callers depend on: an operation that
// never leaves "R" (running) must still surface as a *populated* result plus
// an error that errors.Is(err, ErrConnectionTestTimeout) — not a bare opaque
// error — so a caller (the connection_test data source) can tell "still
// running" apart from a genuine transport/auth/validation failure and choose
// to report success=false instead of aborting the apply.
func TestTestConnection_TimeoutReturnsErrConnectionTestTimeoutAndPartialResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pull_requests"):
			// Operation is created but never reaches a terminal status.
			_, _ = w.Write([]byte(`{"operation_id":"op1","run_id":"run1","status":"R"}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/operations/op1"):
			// Still running, forever — this is the "stuck at R" case observed
			// live against a 5-minute MySQL source test.
			_, _ = w.Write([]byte(`{"status":"R"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := testClient(t, srv)

	// Short timeout + short poll interval so the test runs fast but still
	// exercises at least one poll loop iteration.
	res, err := c.TestConnection(context.Background(), "e1", map[string]any{"task": "get_db_metadata"}, 10*time.Millisecond, 50*time.Millisecond)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !errors.Is(err, ErrConnectionTestTimeout) {
		t.Fatalf("want errors.Is(err, ErrConnectionTestTimeout), got %v", err)
	}
	if res.Status != "R" {
		t.Errorf("Status = %q, want \"R\"", res.Status)
	}
	if res.OperationID != "op1" {
		t.Errorf("OperationID = %q, want \"op1\"", res.OperationID)
	}
}
