package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPutDataFlowVariables_EncryptedFlagReachesAPI pins the wire contract for an
// encrypted data-flow variable.
//
// Observed against the live API (integration, 2026-08-12): the variable settings
// object accepts `is_private` and SILENTLY DROPS `is_encrypted`.
//
//	PUT settings.is_encrypted=true -> 200, echoed settings {"is_private": false}
//	PUT settings.is_private=true   -> 200, echoed settings {"is_private": true}
//
// Because the flag never lands, `is_encrypted = true` in a practitioner's config
// is a no-op, and the API's read (always is_private=false) drives
// Settings.IsEncrypted back to false -> a plan that wants `false -> true` on
// every run and never converges.
//
// The fake server below mimics exactly that behaviour: it honours `is_private`
// and ignores any other key.
func TestPutDataFlowVariables_EncryptedFlagReachesAPI(t *testing.T) {
	var wireBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		wireBody = string(raw)

		var in struct {
			Items []struct {
				Name     string         `json:"name"`
				Value    any            `json:"value"`
				Settings map[string]any `json:"settings"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// The API recognises only is_private inside settings; everything else is
		// dropped without complaint.
		type outSettings struct {
			IsPrivate bool `json:"is_private"`
		}
		type outItem struct {
			Name     string      `json:"name"`
			Value    any         `json:"value"`
			Settings outSettings `json:"settings"`
		}
		out := struct {
			Items []outItem `json:"items"`
		}{}
		for _, it := range in.Items {
			priv, _ := it.Settings["is_private"].(bool)
			out.Items = append(out.Items, outItem{
				Name:     it.Name,
				Value:    it.Value,
				Settings: outSettings{IsPrivate: priv},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	c := testClient(t, srv)

	items := []DataFlowVariable{{
		Name:     "secret_var",
		Value:    "canary-not-a-real-secret",
		Settings: DataFlowVariableSettings{IsEncrypted: true},
	}}

	got, err := c.PutDataFlowVariables(context.Background(), "env1", "flow1", items)
	if err != nil {
		t.Fatalf("PutDataFlowVariables: %v", err)
	}

	// 1. The request must carry the key the API actually honours.
	if !strings.Contains(wireBody, `"is_private":true`) {
		t.Errorf("wire body does not set is_private=true; the API will silently drop the flag.\n  body: %s", wireBody)
	}

	// 2. And must not carry the key the API ignores.
	if strings.Contains(wireBody, `"is_encrypted"`) {
		t.Errorf("wire body still sends is_encrypted, which the API drops.\n  body: %s", wireBody)
	}

	// 3. The flag must survive the round trip, or every plan shows a
	//    perpetual `is_encrypted = false -> true` diff.
	if len(got) != 1 {
		t.Fatalf("expected 1 variable back, got %d", len(got))
	}
	if !got[0].Settings.IsEncrypted {
		t.Errorf("IsEncrypted lost on round trip: got false, want true — this is the perpetual-diff bug")
	}
}
