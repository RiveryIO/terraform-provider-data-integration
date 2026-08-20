package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
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

// dataFrameStaticPlan builds a tftypes.Value from a minimal set of overrides,
// with every other attribute set to null. The connection_settings key must be
// supplied as a tftypes.Value (use tftypes.NewValue(csType, nil) for null).
func dataFrameStaticPlan(t *testing.T, s schema.Schema, overrides map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	objType, ok := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatalf("schema type is not an object")
	}
	attrs := make(map[string]tftypes.Value, len(objType.AttributeTypes))
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

// csType returns the tftypes.Type for the connection_settings nested object.
func csType(t *testing.T, s schema.Schema) tftypes.Type {
	t.Helper()
	objType, _ := s.Type().TerraformType(context.Background()).(tftypes.Object)
	return objType.AttributeTypes["connection_settings"]
}

// ── Schema ────────────────────────────────────────────────────────────────────

// TestDataFrameSchemaConnectionSettingsComputedAndOptional verifies that
// connection_settings is both Optional and Computed. With the types.Object model
// field this is safe: types.Object can represent null / unknown / value. The
// pre-fix *dataFrameConnSettings pointer could only represent null or value —
// it crashed when the framework emitted (unknown) for the absent Computed block:
//
//	Error: Value Conversion Error
//	  Path: connection_settings
//	  Target Type: *provider.dataFrameConnSettings
//	  Suggested Type: basetypes.ObjectValue
//	  Received unknown value, however the target type cannot handle unknown values.
func TestDataFrameSchemaConnectionSettingsComputedAndOptional(t *testing.T) {
	s := dataFrameSchemaForTest(t)
	attr, ok := s.Attributes["connection_settings"]
	if !ok {
		t.Fatal("connection_settings not found in schema")
	}
	nested, ok := attr.(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("connection_settings is not a SingleNestedAttribute, got %T", attr)
	}
	if !nested.IsOptional() {
		t.Error("connection_settings must be Optional (user omits it for internal dataframes)")
	}
	if !nested.IsComputed() {
		t.Error("connection_settings must be Computed (provider fills it in for file-zone dataframes)")
	}
}

// TestDataFrameSchemaNameRequiresReplace verifies name has RequiresReplace.
// The API identifies dataframes by name with no separate cross_id; there is no
// rename endpoint, so a name change must force destroy+recreate (CORE-2391).
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
		t.Error("name must have RequiresReplace — the API has no rename endpoint; rename = forced replace")
	}
}

// ── Plan.Get regression tests (no client, no HTTP) ───────────────────────────

// TestDataFramePlanGetInternalDataframe is the regression test for the
// Optional+Computed crash. An internal dataframe has no connection_settings in
// HCL. With the types.Object model field, the framework can decode (unknown) or
// null into m.ConnectionSettings without panicking.
//
// Before the fix (pointer model), this exact call crashed:
//
//	plan.Get(ctx, &m) → "Received unknown value, however the target type
//	cannot handle unknown values."
func TestDataFramePlanGetInternalDataframe(t *testing.T) {
	ctx := context.Background()
	s := dataFrameSchemaForTest(t)
	ct := csType(t, s)

	// Simulate what the framework emits for an absent Optional+Computed block:
	// the plan value is (unknown), not null.
	planRaw := dataFrameStaticPlan(t, s, map[string]tftypes.Value{
		"id":                  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"environment_id":      tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"name":                tftypes.NewValue(tftypes.String, "tf-gap-test"),
		"connection_settings": tftypes.NewValue(ct, tftypes.UnknownValue), // (unknown) — the pre-fix crash trigger
	})

	plan := tfsdk.Plan{Raw: planRaw, Schema: s}
	var m dataFrameModel
	diags := plan.Get(ctx, &m) // must not crash
	if diags.HasError() {
		t.Fatalf("Plan.Get crashed with unknown connection_settings: %v", diags)
	}
	if !m.ConnectionSettings.IsUnknown() {
		t.Errorf("expected connection_settings to be unknown, got %v", m.ConnectionSettings)
	}
	if m.Name.ValueString() != "tf-gap-test" {
		t.Errorf("name = %q, want %q", m.Name.ValueString(), "tf-gap-test")
	}
}

// TestDataFramePlanGetNullConnectionSettings verifies null (user explicitly
// omitted the block) also decodes without error.
func TestDataFramePlanGetNullConnectionSettings(t *testing.T) {
	ctx := context.Background()
	s := dataFrameSchemaForTest(t)
	ct := csType(t, s)

	planRaw := dataFrameStaticPlan(t, s, map[string]tftypes.Value{
		"id":                  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"environment_id":      tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"name":                tftypes.NewValue(tftypes.String, "tf-internal"),
		"connection_settings": tftypes.NewValue(ct, nil), // null
	})

	plan := tfsdk.Plan{Raw: planRaw, Schema: s}
	var m dataFrameModel
	if diags := plan.Get(ctx, &m); diags.HasError() {
		t.Fatalf("Plan.Get with null connection_settings: %v", diags)
	}
	if !m.ConnectionSettings.IsNull() {
		t.Errorf("expected connection_settings to be null, got %v", m.ConnectionSettings)
	}
}

// TestDataFramePlanGetFilezoneConnectionSettings verifies a fully-set block
// decodes into m.ConnectionSettings and As() extracts the fields correctly.
func TestDataFramePlanGetFilezoneConnectionSettings(t *testing.T) {
	ctx := context.Background()
	s := dataFrameSchemaForTest(t)
	ct := csType(t, s)
	csObjType, _ := ct.(tftypes.Object)

	planRaw := dataFrameStaticPlan(t, s, map[string]tftypes.Value{
		"id":             tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"environment_id": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"name":           tftypes.NewValue(tftypes.String, "tf-fz"),
		"connection_settings": tftypes.NewValue(csObjType, map[string]tftypes.Value{
			"connection":     tftypes.NewValue(tftypes.String, "conn-abc"),
			"datasource_id":  tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			"storage_type":   tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			"default_bucket": tftypes.NewValue(tftypes.String, "my-bucket"),
		}),
	})

	plan := tfsdk.Plan{Raw: planRaw, Schema: s}
	var m dataFrameModel
	if diags := plan.Get(ctx, &m); diags.HasError() {
		t.Fatalf("Plan.Get with file-zone connection_settings: %v", diags)
	}
	if m.ConnectionSettings.IsNull() || m.ConnectionSettings.IsUnknown() {
		t.Fatalf("expected a concrete connection_settings value, got %v", m.ConnectionSettings)
	}

	var cs dataFrameConnSettings
	if diags := m.ConnectionSettings.As(ctx, &cs, basetypes.ObjectAsOptions{}); diags.HasError() { //nolint:exhaustruct  // zero-value options
		t.Fatalf("As() failed: %v", diags)
	}
	if cs.Connection.ValueString() != "conn-abc" {
		t.Errorf("connection = %q, want %q", cs.Connection.ValueString(), "conn-abc")
	}
	if cs.DefaultBucket.ValueString() != "my-bucket" {
		t.Errorf("default_bucket = %q, want %q", cs.DefaultBucket.ValueString(), "my-bucket")
	}
	if !cs.DatasourceID.IsUnknown() {
		t.Errorf("datasource_id should be unknown (not set), got %v", cs.DatasourceID)
	}
}

// ── connSettingsBody ──────────────────────────────────────────────────────────

func TestConnSettingsBodyNull(t *testing.T) {
	cs := types.ObjectNull(dataFrameConnSettingsAttrTypes)
	body, diags := connSettingsBody(context.Background(), cs)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if body != nil {
		t.Errorf("expected nil body for null connection_settings, got %v", body)
	}
}

func TestConnSettingsBodyUnknown(t *testing.T) {
	cs := types.ObjectUnknown(dataFrameConnSettingsAttrTypes)
	body, diags := connSettingsBody(context.Background(), cs)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if body != nil {
		t.Errorf("expected nil body for unknown connection_settings, got %v", body)
	}
}

func TestConnSettingsBodyValue(t *testing.T) {
	ctx := context.Background()
	obj, diags := types.ObjectValueFrom(ctx, dataFrameConnSettingsAttrTypes, dataFrameConnSettings{
		Connection:    types.StringValue("conn-xyz"),
		DatasourceID:  types.StringValue("aws"),
		StorageType:   types.StringValue("s3"),
		DefaultBucket: types.StringValue("bucket-42"),
	})
	if diags.HasError() {
		t.Fatalf("ObjectValueFrom: %v", diags)
	}

	body, d := connSettingsBody(ctx, obj)
	if d.HasError() {
		t.Fatalf("connSettingsBody: %v", d)
	}
	if body["connection"] != "conn-xyz" {
		t.Errorf("connection = %v, want conn-xyz", body["connection"])
	}
	if body["storage_type"] != "s3" {
		t.Errorf("storage_type = %v, want s3", body["storage_type"])
	}
}

// ── apply ─────────────────────────────────────────────────────────────────────

func TestApplyInternalDataframeNullsConnectionSettings(t *testing.T) {
	ctx := context.Background()
	res := &dataFrameResource{}
	m := &dataFrameModel{}

	diags := res.apply(ctx, map[string]any{"name": "my-df"}, "env1", m)
	if diags.HasError() {
		t.Fatalf("apply: %v", diags)
	}
	if !m.ConnectionSettings.IsNull() {
		t.Errorf("expected null connection_settings for internal dataframe, got %v", m.ConnectionSettings)
	}
	if m.Name.ValueString() != "my-df" {
		t.Errorf("name = %q, want my-df", m.Name.ValueString())
	}
}

func TestApplyFilezoneDataframePopulatesConnectionSettings(t *testing.T) {
	ctx := context.Background()
	res := &dataFrameResource{}
	m := &dataFrameModel{}

	api := map[string]any{
		"name": "fz-df",
		"connection_settings": map[string]any{
			"connection":     "conn-1",
			"datasource_id":  "aws",
			"storage_type":   "s3",
			"default_bucket": "bkt",
		},
	}
	diags := res.apply(ctx, api, "env1", m)
	if diags.HasError() {
		t.Fatalf("apply: %v", diags)
	}
	if m.ConnectionSettings.IsNull() || m.ConnectionSettings.IsUnknown() {
		t.Fatalf("expected a concrete connection_settings value, got %v", m.ConnectionSettings)
	}

	var cs dataFrameConnSettings
	if d := m.ConnectionSettings.As(ctx, &cs, basetypes.ObjectAsOptions{}); d.HasError() { //nolint:exhaustruct  // zero-value options
		t.Fatalf("As(): %v", d)
	}
	if cs.Connection.ValueString() != "conn-1" {
		t.Errorf("connection = %q, want conn-1", cs.Connection.ValueString())
	}
	if cs.DatasourceID.ValueString() != "aws" {
		t.Errorf("datasource_id = %q, want aws", cs.DatasourceID.ValueString())
	}
}

// Ensure path is imported (used in other test helpers that read state attributes).
var _ = path.Root
