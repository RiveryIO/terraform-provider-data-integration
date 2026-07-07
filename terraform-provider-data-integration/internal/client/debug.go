package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// logTransport wraps an http.RoundTripper and writes every request and response
// to stderr when DATA_INTEGRATION_DEBUG=1. Activated automatically by New() when
// the env var is set; zero-overhead when it is not.
//
// Output format (one block per round-trip):
//
//	→ POST http://localhost:8008/v1/accounts/abc/environments
//	  Body: {
//	    "environment_name": "tf-acc-env"
//	  }
//	← 201 Created
//	  Body: {
//	    "id": "xyz123",
//	    "environment_name": "tf-acc-env",
//	    "is_deleted": false,
//	    ...
//	  }
//
// The response body is buffered and replaced so the actual caller can still read it.
type logTransport struct {
	base http.RoundTripper
}

func (t *logTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture and re-seal the request body so the real transport can read it.
	var reqSnippet string
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(raw))
		reqSnippet = prettyJSON(raw)
	}

	fmt.Fprintf(os.Stderr, "\n→ %s %s\n", req.Method, req.URL)
	if reqSnippet != "" {
		fmt.Fprintf(os.Stderr, "  Body: %s\n", indentLines(reqSnippet))
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "← ERROR: %v\n", err)
		return resp, err
	}

	// Buffer the response body, re-seal it, then log.
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))

	fmt.Fprintf(os.Stderr, "← %s\n", resp.Status)
	if len(raw) > 0 {
		fmt.Fprintf(os.Stderr, "  Body: %s\n", indentLines(prettyJSON(raw)))
	}
	return resp, nil
}

// prettyJSON returns a pretty-printed version of b if it is valid JSON,
// falling back to the raw bytes as a string when it is not.
func prettyJSON(b []byte) string {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return string(b)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(b)
	}
	return string(out)
}

// indentLines adds two leading spaces to every line after the first so that
// multi-line JSON aligns neatly under the "Body: " label.
func indentLines(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 1 {
		return s
	}
	for i := 1; i < len(lines); i++ {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}
