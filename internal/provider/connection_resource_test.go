package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func connectionSchemaForTest(t *testing.T) schema.Schema {
	t.Helper()
	resp := &resource.SchemaResponse{}
	NewConnectionResource().Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("connection schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// connectionObjectForTest builds a full tftypes object with every attribute
// null, then applies the given overrides -- so a test only has to name the
// attributes it cares about.
func connectionObjectForTest(t *testing.T, s schema.Schema, overrides map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	objType, ok := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatalf("connection schema type is not an object")
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

func newConnectionResource(t *testing.T, srv *httptest.Server) *connectionResource {
	t.Helper()
	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return &connectionResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
}

// ── fingerprintParams ───────────────────────────────────────────────────────────

func TestFingerprintParams(t *testing.T) {
	credsA := `{"authentication_type":"key_pair","private_key_value":"AAA"}`
	credsB := `{"authentication_type":"key_pair","private_key_value":"BBB"}`

	if got := fingerprintParams(jsontypes.NewNormalizedNull()); got != "absent" {
		t.Errorf("null parameters_json: fingerprint = %q, want %q", got, "absent")
	}
	if got := fingerprintParams(jsontypes.NewNormalizedUnknown()); got != "absent" {
		t.Errorf("unknown parameters_json: fingerprint = %q, want %q", got, "absent")
	}

	fpA := fingerprintParams(jsontypes.NewNormalizedValue(credsA))
	fpAAgain := fingerprintParams(jsontypes.NewNormalizedValue(credsA))
	fpB := fingerprintParams(jsontypes.NewNormalizedValue(credsB))

	if fpA != fpAAgain {
		t.Errorf("identical parameters_json must fingerprint identically: %q != %q", fpA, fpAAgain)
	}
	if fpA == fpB {
		t.Errorf("different credentials must fingerprint differently, both got %q", fpA)
	}
	if fpA == "absent" {
		t.Errorf("a real parameters_json value must not collide with the absent-sentinel")
	}
	if len(fpA) != 64 { // hex-encoded sha256
		t.Errorf("fingerprint length = %d, want 64 (hex sha256)", len(fpA))
	}
}

// ── parametersFingerprintPlanModifier: the actual regression test ─────────────
//
// This reproduces the real bug: parameters_json is WriteOnly, so it never
// participates in Terraform's own plan-diff computation. Before this fix, a
// connection that was `import`ed (so state has no fingerprint at all -- it has
// no way to have captured one) and then given real credentials in config
// would plan clean, "No changes", forever, and Update() would never be
// called -- silently leaving the connection with no credentials on the API
// side. The plan modifier must force a mismatch in that exact scenario.

func TestParametersFingerprintPlanModifier(t *testing.T) {
	ctx := context.Background()
	s := connectionSchemaForTest(t)
	realCreds := `{"authentication_type":"key_pair","private_key_value":"a-real-private-key"}`

	t.Run("post-import state (no fingerprint) + config sets credentials => diff detected", func(t *testing.T) {
		// State as it looks immediately after `terraform import`: nothing but
		// id/environment_id populated. parameters_fingerprint is null because
		// it was never computed -- exactly like a freshly imported connection
		// that pre-dates this attribute existing in state.
		stateRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
			"id":             tftypes.NewValue(tftypes.String, "conn1"),
			"environment_id": tftypes.NewValue(tftypes.String, "env1"),
		})
		// Config now declares real credentials.
		configRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
			"id":              tftypes.NewValue(tftypes.String, "conn1"),
			"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
			"name":            tftypes.NewValue(tftypes.String, "Boomi Snowflake"),
			"type":            tftypes.NewValue(tftypes.String, "snowflake"),
			"parameters_json": tftypes.NewValue(tftypes.String, realCreds),
		})
		// Plan carries the prior (null) fingerprint forward by default, since
		// nothing else would touch a Computed attribute the config can't set
		// directly -- that default is exactly the bug this modifier fixes:
		// without it, PlanValue stays null, equals StateValue, and Terraform
		// reports no change at all.
		planRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
			"id":              tftypes.NewValue(tftypes.String, "conn1"),
			"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
			"name":            tftypes.NewValue(tftypes.String, "Boomi Snowflake"),
			"type":            tftypes.NewValue(tftypes.String, "snowflake"),
			"parameters_json": tftypes.NewValue(tftypes.String, nil), // write-only: erased from plan
		})

		req := planmodifier.StringRequest{
			Path:       path.Root("parameters_fingerprint"),
			Config:     tfsdk.Config{Raw: configRaw, Schema: s},
			Plan:       tfsdk.Plan{Raw: planRaw, Schema: s},
			State:      tfsdk.State{Raw: stateRaw, Schema: s},
			StateValue: types.StringNull(),
			PlanValue:  types.StringNull(),
		}
		resp := &planmodifier.StringResponse{PlanValue: types.StringNull()}
		(&parametersFingerprintPlanModifier{}).PlanModifyString(ctx, req, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("plan modifier diagnostics: %v", resp.Diagnostics)
		}
		want := fingerprintParams(jsontypes.NewNormalizedValue(realCreds))
		if !resp.PlanValue.Equal(types.StringValue(want)) {
			t.Errorf("PlanValue = %v, want %v (fingerprint of the configured credentials)", resp.PlanValue, want)
		}
		if resp.PlanValue.Equal(types.StringNull()) {
			t.Fatal("PlanValue must differ from the null state value -- this is the diff that triggers Update()")
		}
	})

	t.Run("unchanged credentials across two plans => no false diff", func(t *testing.T) {
		fp := fingerprintParams(jsontypes.NewNormalizedValue(realCreds))
		stateRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
			"id":                     tftypes.NewValue(tftypes.String, "conn1"),
			"environment_id":         tftypes.NewValue(tftypes.String, "env1"),
			"parameters_fingerprint": tftypes.NewValue(tftypes.String, fp),
		})
		configRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
			"id":              tftypes.NewValue(tftypes.String, "conn1"),
			"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
			"parameters_json": tftypes.NewValue(tftypes.String, realCreds),
		})
		planRaw := stateRaw

		req := planmodifier.StringRequest{
			Path:       path.Root("parameters_fingerprint"),
			Config:     tfsdk.Config{Raw: configRaw, Schema: s},
			Plan:       tfsdk.Plan{Raw: planRaw, Schema: s},
			State:      tfsdk.State{Raw: stateRaw, Schema: s},
			StateValue: types.StringValue(fp),
			PlanValue:  types.StringValue(fp),
		}
		resp := &planmodifier.StringResponse{PlanValue: types.StringValue(fp)}
		(&parametersFingerprintPlanModifier{}).PlanModifyString(ctx, req, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("plan modifier diagnostics: %v", resp.Diagnostics)
		}
		if !resp.PlanValue.Equal(types.StringValue(fp)) {
			t.Errorf("PlanValue = %v, want unchanged fingerprint %v (same credentials, no re-apply needed)", resp.PlanValue, fp)
		}
	})

	t.Run("destroy: plan is null, left untouched", func(t *testing.T) {
		stateRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
			"id":             tftypes.NewValue(tftypes.String, "conn1"),
			"environment_id": tftypes.NewValue(tftypes.String, "env1"),
		})
		req := planmodifier.StringRequest{
			Path:       path.Root("parameters_fingerprint"),
			Config:     tfsdk.Config{Raw: tftypes.NewValue(stateRaw.Type(), nil), Schema: s},
			Plan:       tfsdk.Plan{Raw: tftypes.NewValue(stateRaw.Type(), nil), Schema: s},
			State:      tfsdk.State{Raw: stateRaw, Schema: s},
			StateValue: types.StringNull(),
			PlanValue:  types.StringNull(),
		}
		resp := &planmodifier.StringResponse{PlanValue: types.StringNull()}
		(&parametersFingerprintPlanModifier{}).PlanModifyString(ctx, req, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("plan modifier diagnostics: %v", resp.Diagnostics)
		}
		if !resp.PlanValue.IsNull() {
			t.Errorf("destroy plan: PlanValue = %v, want null (untouched)", resp.PlanValue)
		}
	})
}

// ── Create/Update: parameters_fingerprint is actually stored ──────────────────

func TestConnectionCreateStoresParametersFingerprint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cross_id":        "conn1",
			"connection_name": "Boomi Snowflake",
			"connection_type": "snowflake",
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newConnectionResource(t, srv)
	s := connectionSchemaForTest(t)
	creds := `{"authentication_type":"key_pair","private_key_value":"a-real-private-key"}`

	configRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
		"id":              tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
		"name":            tftypes.NewValue(tftypes.String, "Boomi Snowflake"),
		"type":            tftypes.NewValue(tftypes.String, "snowflake"),
		"parameters_json": tftypes.NewValue(tftypes.String, creds),
	})
	planRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
		"id":                     tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"environment_id":         tftypes.NewValue(tftypes.String, "env1"),
		"name":                   tftypes.NewValue(tftypes.String, "Boomi Snowflake"),
		"type":                   tftypes.NewValue(tftypes.String, "snowflake"),
		"parameters_json":        tftypes.NewValue(tftypes.String, nil), // erased: write-only
		"parameters_fingerprint": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})

	resp := &resource.CreateResponse{
		State: tfsdk.State{Raw: tftypes.NewValue(planRaw.Type(), nil), Schema: s},
	}
	res.Create(ctx, resource.CreateRequest{
		Config: tfsdk.Config{Raw: configRaw, Schema: s},
		Plan:   tfsdk.Plan{Raw: planRaw, Schema: s},
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Create diagnostics: %v", resp.Diagnostics)
	}
	var gotFP types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("parameters_fingerprint"), &gotFP)...)
	want := fingerprintParams(jsontypes.NewNormalizedValue(creds))
	if gotFP.IsNull() || gotFP.ValueString() != want {
		t.Errorf("stored parameters_fingerprint = %v, want %v", gotFP, want)
	}
}

func TestConnectionUpdateRefreshesParametersFingerprint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Guard check inside Update(): connection has no existing secrets,
			// so the update is allowed to proceed without parameters_json.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"cross_id": "conn1"})
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"cross_id":        "conn1",
				"connection_name": "Boomi Snowflake",
				"connection_type": "snowflake",
			})
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	res := newConnectionResource(t, srv)
	s := connectionSchemaForTest(t)
	oldCreds := `{"authentication_type":"key_pair","private_key_value":"old-key"}`
	newCreds := `{"authentication_type":"key_pair","private_key_value":"new-key"}`
	oldFP := fingerprintParams(jsontypes.NewNormalizedValue(oldCreds))

	configRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
		"id":              tftypes.NewValue(tftypes.String, "conn1"),
		"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
		"name":            tftypes.NewValue(tftypes.String, "Boomi Snowflake"),
		"type":            tftypes.NewValue(tftypes.String, "snowflake"),
		"parameters_json": tftypes.NewValue(tftypes.String, newCreds),
	})
	planRaw := connectionObjectForTest(t, s, map[string]tftypes.Value{
		"id":                     tftypes.NewValue(tftypes.String, "conn1"),
		"environment_id":         tftypes.NewValue(tftypes.String, "env1"),
		"name":                   tftypes.NewValue(tftypes.String, "Boomi Snowflake"),
		"type":                   tftypes.NewValue(tftypes.String, "snowflake"),
		"parameters_json":        tftypes.NewValue(tftypes.String, nil), // erased: write-only
		"parameters_fingerprint": tftypes.NewValue(tftypes.String, "recomputed-by-modifier-in-real-terraform"),
	})

	resp := &resource.UpdateResponse{
		State: tfsdk.State{Raw: tftypes.NewValue(planRaw.Type(), nil), Schema: s},
	}
	res.Update(ctx, resource.UpdateRequest{
		Config: tfsdk.Config{Raw: configRaw, Schema: s},
		Plan:   tfsdk.Plan{Raw: planRaw, Schema: s},
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update diagnostics: %v", resp.Diagnostics)
	}
	var gotFP types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("parameters_fingerprint"), &gotFP)...)
	want := fingerprintParams(jsontypes.NewNormalizedValue(newCreds))
	if gotFP.ValueString() != want {
		t.Errorf("stored parameters_fingerprint = %v, want %v (fingerprint of the NEW credentials)", gotFP, want)
	}
	if gotFP.ValueString() == oldFP {
		t.Error("fingerprint must change when credentials change")
	}
}
