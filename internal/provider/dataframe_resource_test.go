package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func dataFrameSchemaForTest(t *testing.T) schema.Schema {
	t.Helper()
	resp := &resource.SchemaResponse{}
	NewDataFrameResource().Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("dataframe schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// dataFrameObjectForTest builds a full tftypes.Object with every attribute null,
// then applies the given overrides. Callers only need to specify the fields they
// care about; every other field starts as null.
func dataFrameObjectForTest(t *testing.T, s schema.Schema, overrides map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	objType, ok := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatalf("dataframe schema type is not an object")
	}
	attrs := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		attrs[name] = tftypes.NewValue(typ, nil)
	}
	for name, v := range overrides {
		if _, ok := attrs[name]; !ok {
			t.Fatalf("unknown attribute %q in schema", name)
		}
		attrs[name] = v
	}
	return tftypes.NewValue(objType, attrs)
}

// dataFrameConnSettingsType returns the tftypes.Type for the connection_settings
// nested block so callers can build null or filled values for it.
func dataFrameConnSettingsType(t *testing.T, s schema.Schema) tftypes.Type {
	t.Helper()
	objType, _ := s.Type().TerraformType(context.Background()).(tftypes.Object)
	return objType.AttributeTypes["connection_settings"]
}

func newDataFrameResource(t *testing.T, srv *httptest.Server) *dataFrameResource {
	t.Helper()
	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return &dataFrameResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
}

// ── Schema ────────────────────────────────────────────────────────────────────

// TestDataFrameSchemaConnectionSettingsNotComputed is the schema-level assertion
// for the Optional+Computed crash. Root cause and fix documented in the test body.
//
// Bug (pre-fix): connection_settings was Optional+Computed on a
// SingleNestedAttribute whose model field is *dataFrameConnSettings. The Plugin
// Framework emits (unknown) — not null — for an absent Optional+Computed block
// because Computed signals "the provider may fill this in". *dataFrameConnSettings
// is a plain Go pointer: it can represent nil (null) or a value, but NOT (unknown).
// The framework panics on req.Plan.Get() in Create:
//
//	Error: Value Conversion Error
//	  Path: connection_settings
//	  Target Type: *provider.dataFrameConnSettings
//	  Suggested Type: basetypes.ObjectValue
//	  Received unknown value, however the target type cannot handle unknown values.
//
// Fix: remove Computed:true from the parent block. An absent Optional-only block is
// null (not unknown) in the plan → pointer is nil → no crash. Sub-fields stay
// Optional+Computed because they use types.String, which CAN hold unknowns.
func TestDataFrameSchemaConnectionSettingsNotComputed(t *testing.T) {
	s := dataFrameSchemaForTest(t)
	attr, ok := s.Attributes["connection_settings"]
	if !ok {
		t.Fatal("connection_settings not found in schema")
	}
	nested, ok := attr.(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("connection_settings is not a SingleNestedAttribute, got %T", attr)
	}
	if nested.IsComputed() {
		t.Error("connection_settings must NOT be Computed — see test doc for the full crash scenario")
	}
	if !nested.IsOptional() {
		t.Error("connection_settings should be Optional (user omits it for internal dataframes)")
	}
}

// TestDataFrameSchemaNameRequiresReplace verifies that changing name triggers a
// forced replace. The API identifies dataframes by name with no separate cross_id,
// so there is no rename-in-place — destroy+recreate is the only path (CORE-2391).
func TestDataFrameSchemaNameRequiresReplace(t *testing.T) {
	s := dataFrameSchemaForTest(t)
	attr, ok := s.Attributes["name"]
	if !ok {
		t.Fatal("name not found in schema")
	}
	str, ok := attr.(schema.StringAttribute)
	if !ok {
		t.Fatalf("name is not a StringAttribute, got %T", attr)
	}
	if len(str.PlanModifiers) == 0 {
		t.Error("name must have RequiresReplace PlanModifier — the API has no rename endpoint; rename forces destroy+recreate")
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

// TestDataFrameCreateInternalNoCrash is the end-to-end regression test for the
// Optional+Computed crash. An internal dataframe has no connection_settings in
// HCL (null in config). With the fix (Computed removed), the plan holds null →
// *dataFrameConnSettings is nil → Create proceeds normally.
func TestDataFrameCreateInternalNoCrash(t *testing.T) {
	apiResponse := map[string]any{"name": "tf-gap-test"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newDataFrameResource(t, srv)
	s := dataFrameSchemaForTest(t)
	csType := dataFrameConnSettingsType(t, s)

	planRaw := dataFrameObjectForTest(t, s, map[string]tftypes.Value{
		"id":                  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"environment_id":      tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"name":                tftypes.NewValue(tftypes.String, "tf-gap-test"),
		"connection_settings": tftypes.NewValue(csType, nil), // null — internal dataframe
	})

	resp := &resource.CreateResponse{State: tfsdk.State{Raw: tftypes.NewValue(planRaw.Type(), nil), Schema: s}}
	// Before the fix this crashed: "Received unknown value, however the target type
	// cannot handle unknown values." This call must complete without error.
	res.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: planRaw, Schema: s}}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create with no connection_settings returned diagnostics: %v", resp.Diagnostics)
	}

	// State must record the name returned by the API.
	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if id.ValueString() != "tf-gap-test" {
		t.Errorf("state id = %q, want %q", id.ValueString(), "tf-gap-test")
	}
}

// TestDataFrameCreateFilezonePopulatesSettingsFromAPI verifies that a file-zone
// dataframe (connection_settings present in config) sends the block to the API
// and populates state from the API response.
func TestDataFrameCreateFilezonePopulatesSettingsFromAPI(t *testing.T) {
	apiResponse := map[string]any{
		"name": "tf-fz-test",
		"connection_settings": map[string]any{
			"connection":     "conn-abc",
			"datasource_id":  "aws",
			"storage_type":   "s3",
			"default_bucket": "my-bucket",
		},
	}
	var sentBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sentBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newDataFrameResource(t, srv)
	s := dataFrameSchemaForTest(t)

	csType := dataFrameConnSettingsType(t, s)
	csObjType, _ := csType.(tftypes.Object)
	csValue := tftypes.NewValue(csObjType, map[string]tftypes.Value{
		"connection":     tftypes.NewValue(tftypes.String, "conn-abc"),
		"datasource_id":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"storage_type":   tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"default_bucket": tftypes.NewValue(tftypes.String, "my-bucket"),
	})

	planRaw := dataFrameObjectForTest(t, s, map[string]tftypes.Value{
		"id":                  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"environment_id":      tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"name":                tftypes.NewValue(tftypes.String, "tf-fz-test"),
		"connection_settings": csValue,
	})

	resp := &resource.CreateResponse{State: tfsdk.State{Raw: tftypes.NewValue(planRaw.Type(), nil), Schema: s}}
	res.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: planRaw, Schema: s}}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if sentBody["connection_settings"] == nil {
		t.Error("Create did not send connection_settings to API")
	}

	// State must reflect the connection ID returned by the API.
	var connID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx,
		path.Root("connection_settings").AtName("connection"), &connID)...)
	if connID.ValueString() != "conn-abc" {
		t.Errorf("state connection_settings.connection = %q, want %q", connID.ValueString(), "conn-abc")
	}
}

// ── Read ──────────────────────────────────────────────────────────────────────

// TestDataFrameReadInternalKeepsConnectionSettingsNull verifies that reading an
// internal dataframe (API returns no connection_settings) leaves the state block null.
func TestDataFrameReadInternalKeepsConnectionSettingsNull(t *testing.T) {
	apiResponse := map[string]any{"name": "tf-gap-test"} // no connection_settings in API response
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResponse)
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newDataFrameResource(t, srv)
	s := dataFrameSchemaForTest(t)
	csType := dataFrameConnSettingsType(t, s)

	stateRaw := dataFrameObjectForTest(t, s, map[string]tftypes.Value{
		"id":                  tftypes.NewValue(tftypes.String, "tf-gap-test"),
		"environment_id":      tftypes.NewValue(tftypes.String, "env1"),
		"name":                tftypes.NewValue(tftypes.String, "tf-gap-test"),
		"connection_settings": tftypes.NewValue(csType, nil),
	})

	resp := &resource.ReadResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	res.Read(ctx, resource.ReadRequest{State: tfsdk.State{Raw: stateRaw, Schema: s}}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	// connection_settings must remain null — the API returned none.
	var cs *dataFrameConnSettings
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("connection_settings"), &cs)...)
	if cs != nil {
		t.Errorf("connection_settings should be nil after Read of internal dataframe, got %+v", cs)
	}
}
