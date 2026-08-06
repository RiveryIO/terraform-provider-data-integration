package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func cdcConfigSchemaForTest(t *testing.T) schema.Schema {
	t.Helper()
	resp := &resource.SchemaResponse{}
	NewCDCConfigResource().Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("cdc config schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// cdcConfigObjectForTest builds a full tftypes object with every attribute null,
// then applies the given overrides.
func cdcConfigObjectForTest(t *testing.T, s schema.Schema, overrides map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	objType, ok := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatalf("cdc config schema type is not an object")
	}
	attrs := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		attrs[name] = tftypes.NewValue(typ, nil)
	}
	for name, v := range overrides {
		if _, ok := attrs[name]; !ok {
			t.Fatalf("unknown attribute %q", name)
		}
		attrs[name] = v
	}
	return tftypes.NewValue(objType, attrs)
}

// cdcConfigStateForTest builds a state tftypes value with the given field values.
func cdcConfigStateForTest(t *testing.T, s schema.Schema, dataFlowID, envID string, configJSON tftypes.Value) tftypes.Value {
	t.Helper()
	return cdcConfigObjectForTest(t, s, map[string]tftypes.Value{
		"id":            tftypes.NewValue(tftypes.String, dataFlowID),
		"data_flow_id":  tftypes.NewValue(tftypes.String, dataFlowID),
		"environment_id": tftypes.NewValue(tftypes.String, envID),
		"config_json":   configJSON,
	})
}

// cdcConfigPlanForTest builds a create-plan tftypes value (computed fields unknown).
func cdcConfigPlanForTest(t *testing.T, s schema.Schema, dataFlowID string, configJSON tftypes.Value) tftypes.Value {
	t.Helper()
	return cdcConfigObjectForTest(t, s, map[string]tftypes.Value{
		"id":            tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"data_flow_id":  tftypes.NewValue(tftypes.String, dataFlowID),
		"environment_id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"config_json":   configJSON,
	})
}

func newCDCConfigResource(t *testing.T, srv *httptest.Server) *cdcConfigResource {
	t.Helper()
	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return &cdcConfigResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
}

// ── Read ──────────────────────────────────────────────────────────────────────

func TestCDCConfigReadPopulatesConfigJSONFromAPI(t *testing.T) {
	apiConfig := map[string]any{
		"config": map[string]any{
			"datasource_type": "mysql",
			"binlog_file":     "mysql-bin.000001",
			"binlog_position": "12345",
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiConfig)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newCDCConfigResource(t, srv)
	s := cdcConfigSchemaForTest(t)

	stateRaw := cdcConfigStateForTest(t, s, "flow1", "env1", tftypes.NewValue(tftypes.String, nil))
	resp := &resource.ReadResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	res.Read(ctx, resource.ReadRequest{State: tfsdk.State{Raw: stateRaw, Schema: s}}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var gotJSON jsontypes.Normalized
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("config_json"), &gotJSON)...)
	if gotJSON.IsNull() {
		t.Fatal("config_json should be populated after Read, got null")
	}
	// Inner object must be present.
	var inner map[string]any
	if err := json.Unmarshal([]byte(gotJSON.ValueString()), &inner); err != nil {
		t.Fatalf("config_json is not valid JSON: %v", err)
	}
	if inner["datasource_type"] != "mysql" {
		t.Errorf("datasource_type = %v, want mysql", inner["datasource_type"])
	}
}

func TestCDCConfigReadKeepsStateWhenOffsetNotMaterialized(t *testing.T) {
	// API returns 400 — offset not yet materialized.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"CDC config not found"}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newCDCConfigResource(t, srv)
	s := cdcConfigSchemaForTest(t)

	// State already has a previously-seeded config_json.
	priorJSON := `{"datasource_type":"mysql","binlog_file":"mysql-bin.000001","binlog_position":"100"}`
	stateRaw := cdcConfigStateForTest(t, s, "flow1", "env1", tftypes.NewValue(tftypes.String, priorJSON))
	resp := &resource.ReadResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	res.Read(ctx, resource.ReadRequest{State: tfsdk.State{Raw: stateRaw, Schema: s}}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var gotJSON jsontypes.Normalized
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("config_json"), &gotJSON)...)
	if gotJSON.IsNull() {
		t.Error("config_json should be preserved when API returns 400 (offset not materialized)")
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestCDCConfigCreateWithConfigJSONCallsSetAPI(t *testing.T) {
	var calls []string
	var receivedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newCDCConfigResource(t, srv)
	s := cdcConfigSchemaForTest(t)

	configJSON := `{"datasource_type":"mysql","binlog_file":"mysql-bin.000001","binlog_position":"42"}`
	planRaw := cdcConfigPlanForTest(t, s, "flow1", tftypes.NewValue(tftypes.String, configJSON))
	resp := &resource.CreateResponse{State: tfsdk.State{Raw: tftypes.NewValue(planRaw.Type(), nil), Schema: s}}
	res.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: planRaw, Schema: s}}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if len(calls) != 1 || !strings.HasSuffix(calls[0], "/cdc_config") {
		t.Errorf("expected one POST to .../cdc_config, got %v", calls)
	}
	if _, ok := receivedBody["config"]; !ok {
		t.Errorf("POST body missing 'config' envelope key, got %v", receivedBody)
	}

	// ID must be set in state.
	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if id.ValueString() != "flow1" {
		t.Errorf("state id = %q, want %q", id.ValueString(), "flow1")
	}
}

func TestCDCConfigCreateWithoutConfigJSONSkipsSetAPI(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newCDCConfigResource(t, srv)
	s := cdcConfigSchemaForTest(t)

	// config_json null (not set in configuration).
	planRaw := cdcConfigPlanForTest(t, s, "flow1", tftypes.NewValue(tftypes.String, nil))
	resp := &resource.CreateResponse{State: tfsdk.State{Raw: tftypes.NewValue(planRaw.Type(), nil), Schema: s}}
	res.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: planRaw, Schema: s}}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if len(calls) != 0 {
		t.Errorf("expected no API calls when config_json is null, got %v", calls)
	}

	// ID must still be set in state.
	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if id.ValueString() != "flow1" {
		t.Errorf("state id = %q, want %q", id.ValueString(), "flow1")
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestCDCConfigUpdateWithConfigJSONCallsSetAPI(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newCDCConfigResource(t, srv)
	s := cdcConfigSchemaForTest(t)

	configJSON := `{"datasource_type":"mysql","binlog_file":"mysql-bin.000002","binlog_position":"99"}`
	stateRaw := cdcConfigStateForTest(t, s, "flow1", "env1", tftypes.NewValue(tftypes.String, nil))
	planRaw := cdcConfigStateForTest(t, s, "flow1", "env1", tftypes.NewValue(tftypes.String, configJSON))

	resp := &resource.UpdateResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	res.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Raw: planRaw, Schema: s},
		State: tfsdk.State{Raw: stateRaw, Schema: s},
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if len(calls) != 1 || !strings.HasSuffix(calls[0], "/cdc_config") {
		t.Errorf("expected one POST to .../cdc_config, got %v", calls)
	}
}

func TestCDCConfigUpdateWithoutConfigJSONSkipsSetAPI(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newCDCConfigResource(t, srv)
	s := cdcConfigSchemaForTest(t)

	stateRaw := cdcConfigStateForTest(t, s, "flow1", "env1", tftypes.NewValue(tftypes.String, nil))
	// Plan also has config_json null (user never set it).
	planRaw := cdcConfigStateForTest(t, s, "flow1", "env1", tftypes.NewValue(tftypes.String, nil))

	resp := &resource.UpdateResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	res.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Raw: planRaw, Schema: s},
		State: tfsdk.State{Raw: stateRaw, Schema: s},
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if len(calls) != 0 {
		t.Errorf("expected no API calls when config_json is null, got %v", calls)
	}
}
