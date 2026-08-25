package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// catalogServer serves a /v1/connections_types listing shaped like the real one:
// a paginated { items: [ { fields: {...} } ] } envelope, with more than one row
// per connection_type so the union-across-rows path is exercised.
func catalogServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	page1 := map[string]any{
		"total_items": 3,
		"items": []any{
			map[string]any{"fields": map[string]any{
				"connection_type": "mysql",
				"properties": []any{
					map[string]any{"id": "host", "type": "string"},
					map[string]any{"id": "port", "type": "integer"},
					map[string]any{"id": "password", "type": "password"},
				},
			}},
			// Second mysql row contributing extra ids — the real catalog does
			// this, and the per-type endpoint is the one that under-reports.
			map[string]any{"fields": map[string]any{
				"connection_type": "mysql",
				"properties": []any{
					map[string]any{"id": "is_ssh_tunnel", "type": "boolean"},
					map[string]any{"id": "ssh_remote_user", "type": "string"},
				},
			}},
			map[string]any{"fields": map[string]any{
				"connection_type": "snowflake",
				"properties":      []any{map[string]any{"id": "account_name", "type": "string"}},
			}},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.RawQuery, "page=1") {
			_ = json.NewEncoder(w).Encode(page1)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
}

// planConnection runs ModifyPlan for a connection whose config carries connType
// and paramsJSON, and returns the warnings raised.
func planConnection(t *testing.T, srv *httptest.Server, c *client.Client, connType, paramsJSON string) []string {
	t.Helper()
	ctx := context.Background()

	r := &connectionResource{data: &providerData{client: c}}
	schemaResp := &resource.SchemaResponse{}
	r.Schema(ctx, resource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	s := schemaResp.Schema
	objType := s.Type().TerraformType(ctx).(tftypes.Object)

	nullMap := tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil)
	cfg := tftypes.NewValue(objType, map[string]tftypes.Value{
		"id":                            tftypes.NewValue(tftypes.String, nil),
		"environment_id":                tftypes.NewValue(tftypes.String, nil),
		"name":                          tftypes.NewValue(tftypes.String, "probe"),
		"type":                          tftypes.NewValue(tftypes.String, connType),
		"parameters_json":               tftypes.NewValue(tftypes.String, paramsJSON),
		"fz_connection_id":              tftypes.NewValue(tftypes.String, nil),
		"connection_info":               tftypes.NewValue(tftypes.String, nil),
		"file_params":                   nullMap,
		"file_params_content":           nullMap,
		"file_params_content_filenames": nullMap,
		"file_param_paths":              nullMap,
		"ssh_pkey_file":                 tftypes.NewValue(tftypes.String, nil),
		"ssh_pkey_file_path":            tftypes.NewValue(tftypes.String, nil),
	})

	req := resource.ModifyPlanRequest{
		Config: tfsdk.Config{Schema: s, Raw: cfg},
		Plan:   tfsdk.Plan{Schema: s, Raw: cfg},
		State:  tfsdk.State{Schema: s, Raw: tftypes.NewValue(objType, nil)},
	}
	resp := &resource.ModifyPlanResponse{
		Plan: tfsdk.Plan{Schema: s, Raw: cfg},
	}
	r.ModifyPlan(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan returned errors, expected warnings only: %v", resp.Diagnostics)
	}

	var warnings []string
	for _, d := range resp.Diagnostics.Warnings() {
		warnings = append(warnings, d.Summary()+": "+d.Detail())
	}
	return warnings
}

func catalogClient(t *testing.T, srv *httptest.Server) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "tok", AccountID: "acct1"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c
}

// TestModifyPlanAcceptsValidKeys covers the quiet path: every key is a real
// property id, including one contributed only by the catalog's SECOND mysql row.
// A per-type-endpoint implementation would wrongly flag is_ssh_tunnel here.
func TestModifyPlanAcceptsValidKeys(t *testing.T) {
	calls := 0
	srv := catalogServer(t, &calls)
	defer srv.Close()

	warnings := planConnection(t, srv, catalogClient(t, srv), "mysql", `{
		"host": "db.example.com", "port": 3306, "password": "x",
		"is_ssh_tunnel": true, "ssh_remote_user": "tunnel"
	}`)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
}

// TestModifyPlanWarnsOnMisspelledKey is the case this whole check exists for:
// the API would accept and discard it, and no later read could reveal that.
func TestModifyPlanWarnsOnMisspelledKey(t *testing.T) {
	calls := 0
	srv := catalogServer(t, &calls)
	defer srv.Close()

	warnings := planConnection(t, srv, catalogClient(t, srv), "mysql",
		`{"host": "db.example.com", "passwordd": "x"}`)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	for _, want := range []string{"passwordd", "Unrecognised parameters_json keys", "host"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning missing %q:\n%s", want, w)
		}
	}
	// It must not flag the correctly-spelled key.
	if strings.Contains(w, "  host, passwordd") {
		t.Errorf("warning wrongly lists host as unrecognised:\n%s", w)
	}
}

// TestModifyPlanWarnsOnWrapperObject covers the nesting trap: a whole body one
// level too deep is accepted by the API and produces an empty connection.
func TestModifyPlanWarnsOnWrapperObject(t *testing.T) {
	calls := 0
	srv := catalogServer(t, &calls)
	defer srv.Close()

	warnings := planConnection(t, srv, catalogClient(t, srv), "mysql",
		`{"connection_configuration": {"host": "db.example.com", "password": "x"}}`)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "must be FLAT") {
		t.Errorf("wrapper warning does not explain flatness:\n%s", warnings[0])
	}
	if !strings.Contains(warnings[0], "connection_configuration") {
		t.Errorf("wrapper warning does not name the wrapper key:\n%s", warnings[0])
	}
}

// TestModifyPlanSkipsUnlistedType asserts the check is a safety net, not a gate:
// a connection type absent from the catalog is left alone rather than rejected.
func TestModifyPlanSkipsUnlistedType(t *testing.T) {
	calls := 0
	srv := catalogServer(t, &calls)
	defer srv.Close()

	warnings := planConnection(t, srv, catalogClient(t, srv), "brand_new_connector",
		`{"whatever": "x"}`)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for an unlisted type, got: %v", warnings)
	}
}

// TestModifyPlanSurvivesCatalogFailure asserts an unreachable catalog degrades to
// no check rather than blocking the plan.
func TestModifyPlanSurvivesCatalogFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := client.New(client.Config{
		BaseURL: srv.URL, Token: "tok", AccountID: "acct1", MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	warnings := planConnection(t, srv, c, "mysql", `{"passwordd": "x"}`)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when the catalog is down, got: %v", warnings)
	}
}

// TestConnectionTypePropertiesCachedOnce asserts the catalog is fetched at most
// once per client. It costs ~19 paginated requests live, so re-fetching it per
// connection resource would be a real regression.
func TestConnectionTypePropertiesCachedOnce(t *testing.T) {
	calls := 0
	srv := catalogServer(t, &calls)
	defer srv.Close()
	c := catalogClient(t, srv)
	ctx := context.Background()

	mysql, ok, err := c.ConnectionTypeProperties(ctx, "mysql")
	if err != nil || !ok {
		t.Fatalf("first lookup: ok=%v err=%v", ok, err)
	}
	want := []string{"host", "is_ssh_tunnel", "password", "port", "ssh_remote_user"}
	if !equalStrings(mysql, want) {
		t.Errorf("mysql properties = %v, want %v (sorted union across catalog rows)", mysql, want)
	}
	afterFirst := calls

	if _, _, err := c.ConnectionTypeProperties(ctx, "snowflake"); err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if _, _, err := c.ConnectionTypeProperties(ctx, "mysql"); err != nil {
		t.Fatalf("third lookup: %v", err)
	}
	if calls != afterFirst {
		t.Errorf("catalog re-fetched: %d calls after first lookup, %d after three", afterFirst, calls)
	}

	if _, ok, _ := c.ConnectionTypeProperties(ctx, "nope"); ok {
		t.Error("unlisted type reported as known")
	}
}
