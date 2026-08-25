package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// readConnectionType drives the data source's Read against srv, returning the
// resulting state. It exercises the real schema and the real Read path, so an
// attribute that is declared but never populated (or vice versa) fails here —
// which is the whole point of covering the property_schema_json rename without
// live credentials.
func readConnectionType(t *testing.T, srv *httptest.Server, connType string) connectionTypeModel {
	t.Helper()
	ctx := context.Background()

	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "tok", AccountID: "acct1"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ds := &connectionTypeDataSource{data: &providerData{client: c}}

	schemaResp := &datasource.SchemaResponse{}
	ds.Schema(ctx, datasource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	s := schemaResp.Schema
	objType := s.Type().TerraformType(ctx).(tftypes.Object)

	cfgVal := tftypes.NewValue(objType, map[string]tftypes.Value{
		"id":                   tftypes.NewValue(tftypes.String, nil),
		"connection_type":      tftypes.NewValue(tftypes.String, connType),
		"connection_type_name": tftypes.NewValue(tftypes.String, nil),
		"property_names":       tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, nil),
		"property_schema_json": tftypes.NewValue(tftypes.String, nil),
		"properties_json":      tftypes.NewValue(tftypes.String, nil),
	})

	req := datasource.ReadRequest{Config: tfsdk.Config{Schema: s, Raw: cfgVal}}
	resp := &datasource.ReadResponse{
		State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(objType, nil)},
	}
	ds.Read(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}

	var out connectionTypeModel
	if diags := resp.State.Get(ctx, &out); diags.HasError() {
		t.Fatalf("state.Get: %v", diags)
	}
	return out
}

// TestConnectionTypePropertySchemaJSON covers the rename's contract: the new
// attribute carries the raw property schema, and the deprecated alias carries
// the identical value so existing configurations keep working.
func TestConnectionTypePropertySchemaJSON(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		// Trimmed shape of a real GET /v1/connections_types/{type} body.
		_, _ = w.Write([]byte(`{
			"connection_type": "mysql",
			"connection_type_name": "MySQL",
			"properties": [
				{"id": "host", "type": "string", "display_name": "Host"},
				{"id": "port", "type": "number", "default_value": 3306},
				{"id": "password", "type": "string", "ui_type": "password"}
			]
		}`))
	}))
	defer srv.Close()

	state := readConnectionType(t, srv, "mysql")

	if want := "/v1/connections_types/mysql"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
	if got := state.ConnectionTypeName.ValueString(); got != "MySQL" {
		t.Errorf("connection_type_name = %q, want %q", got, "MySQL")
	}

	schemaJSON := state.PropertySchemaJSON.ValueString()
	for _, want := range []string{`"id":"host"`, `"id":"port"`, `"id":"password"`, `"default_value":3306`} {
		if !contains(schemaJSON, want) {
			t.Errorf("property_schema_json %q missing %q", schemaJSON, want)
		}
	}

	// The deprecated alias must stay byte-identical, not merely non-empty:
	// that equality is the backwards-compatibility promise.
	if alias := state.PropertiesJSON.ValueString(); alias != schemaJSON {
		t.Errorf("properties_json = %q, want it identical to property_schema_json %q", alias, schemaJSON)
	}

	var names []string
	if diags := state.PropertyNames.ElementsAs(context.Background(), &names, false); diags.HasError() {
		t.Fatalf("property_names: %v", diags)
	}
	if want := []string{"host", "port", "password"}; !equalStrings(names, want) {
		t.Errorf("property_names = %v, want %v", names, want)
	}
}

// TestConnectionTypeNoProperties covers the empty branch: both attributes fall
// back to "[]" rather than one of them being left null.
func TestConnectionTypeNoProperties(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"connection_type":"weird","connection_type_name":"Weird"}`))
	}))
	defer srv.Close()

	state := readConnectionType(t, srv, "weird")

	if got := state.PropertySchemaJSON.ValueString(); got != "[]" {
		t.Errorf("property_schema_json = %q, want %q", got, "[]")
	}
	if got := state.PropertiesJSON.ValueString(); got != "[]" {
		t.Errorf("properties_json = %q, want %q", got, "[]")
	}
}

// TestConnectionTypeAliasDeprecated guards the deprecation itself: the old name
// must stay present (so configs keep working) and must stay marked deprecated
// (so the registry page tells people to move).
func TestConnectionTypeAliasDeprecated(t *testing.T) {
	ctx := context.Background()
	ds := &connectionTypeDataSource{}
	resp := &datasource.SchemaResponse{}
	ds.Schema(ctx, datasource.SchemaRequest{}, resp)

	attrs := resp.Schema.Attributes
	if _, ok := attrs["property_schema_json"]; !ok {
		t.Fatal("property_schema_json attribute missing")
	}
	alias, ok := attrs["properties_json"]
	if !ok {
		t.Fatal("properties_json alias removed — that is a breaking change, not a deprecation")
	}
	if alias.GetDeprecationMessage() == "" {
		t.Error("properties_json has no deprecation message")
	}
	if !contains(alias.GetDeprecationMessage(), "property_schema_json") {
		t.Error("properties_json deprecation message does not name its replacement")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
