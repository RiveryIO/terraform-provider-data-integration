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
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ── CDC detection ─────────────────────────────────────────────────────────────

func TestIsCDCFlow(t *testing.T) {
	cases := []struct {
		name  string
		props string
		want  bool
	}{
		{"log based", `{"source":{"additional_settings":{"extract_method":"log"}}}`, true},
		{"incremental", `{"source":{"additional_settings":{"extract_method":"incremental"}}}`, false},
		{"all", `{"source":{"additional_settings":{"extract_method":"all"}}}`, false},
		{"no extract_method", `{"source":{"additional_settings":{}}}`, false},
		{"no additional_settings", `{"source":{}}`, false},
		{"no source", `{"properties_type":"logic"}`, false},
		{"empty", ``, false},
		{"not json", `nope`, false},
		{"wrong nesting — per-table extract_method is not the flow-level one",
			`{"source":{"schemas":[{"tables":[{"details":{"extract_method":"log"}}]}]}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCDCFlow(tc.props); got != tc.want {
				t.Errorf("isCDCFlow(%s) = %v, want %v", tc.props, got, tc.want)
			}
		})
	}
}

// ── river_status mapping (including the absent case) ──────────────────────────

func TestStatusFromAPI(t *testing.T) {
	cases := []struct {
		name string
		api  map[string]any
		want types.String
	}{
		{"active", map[string]any{"metadata": map[string]any{"river_status": "active"}}, types.StringValue("active")},
		{"disabled", map[string]any{"metadata": map[string]any{"river_status": "disabled"}}, types.StringValue("disabled")},
		{"river_status absent from metadata", map[string]any{"metadata": map[string]any{"description": "x"}}, types.StringNull()},
		{"metadata absent", map[string]any{"name": "x"}, types.StringNull()},
		{"metadata null", map[string]any{"metadata": nil}, types.StringNull()},
		{"metadata not an object", map[string]any{"metadata": "nope"}, types.StringNull()},
		{"river_status null", map[string]any{"metadata": map[string]any{"river_status": nil}}, types.StringNull()},
		{"empty body", map[string]any{}, types.StringNull()},
		{"nil body", nil, types.StringNull()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusFromAPI(tc.api); !got.Equal(tc.want) {
				t.Errorf("statusFromAPI() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestActiveFromRiverStatus(t *testing.T) {
	cases := []struct {
		name       string
		api        map[string]any
		wantActive bool
		wantOK     bool
	}{
		{"active", map[string]any{"metadata": map[string]any{"river_status": "active"}}, true, true},
		{"disabled", map[string]any{"metadata": map[string]any{"river_status": "disabled"}}, false, true},
		// Absent river_status must not be reported as "disabled": the caller keeps
		// the previously known activate value instead of writing a bogus one.
		{"absent", map[string]any{"metadata": map[string]any{}}, false, false},
		{"no metadata", map[string]any{}, false, false},
		{"nil body", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			active, ok := activeFromRiverStatus(tc.api)
			if active != tc.wantActive || ok != tc.wantOK {
				t.Errorf("activeFromRiverStatus() = (%v, %v), want (%v, %v)", active, ok, tc.wantActive, tc.wantOK)
			}
		})
	}
}

// ── status plan/apply consistency ─────────────────────────────────────────────

func TestResolveStatus(t *testing.T) {
	activeBody := map[string]any{"metadata": map[string]any{"river_status": "active"}}
	disabledBody := map[string]any{"metadata": map[string]any{"river_status": "disabled"}}
	noStatusBody := map[string]any{"metadata": map[string]any{}}

	cases := []struct {
		name        string
		planned     types.String
		api         map[string]any
		activated   bool
		deactivated bool
		want        types.String
	}{
		// A concrete planned value always wins — returning anything else is a
		// "Provider produced inconsistent result after apply" error.
		{"planned known wins over api", types.StringValue("active"), disabledBody, false, false, types.StringValue("active")},
		{"planned known wins over activated", types.StringValue("active"), disabledBody, true, false, types.StringValue("active")},
		{"planned null is a concrete value and is kept", types.StringNull(), activeBody, true, false, types.StringNull()},
		// Unknown (create, or an apply that changes activation).
		{"unknown + activated", types.StringUnknown(), disabledBody, true, false, types.StringValue("active")},
		{"unknown + deactivated", types.StringUnknown(), activeBody, false, true, types.StringValue("disabled")},
		{"unknown + no activation falls back to the response", types.StringUnknown(), disabledBody, false, false, types.StringValue("disabled")},
		{"unknown + response without river_status", types.StringUnknown(), noStatusBody, false, false, types.StringNull()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveStatus(tc.planned, tc.api, tc.activated, tc.deactivated)
			if !got.Equal(tc.want) {
				t.Errorf("resolveStatus() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStatusMayChange(t *testing.T) {
	cases := []struct {
		name          string
		planActivate  types.Bool
		stateActivate types.Bool
		stateStatus   types.String
		want          bool
	}{
		{"activating", types.BoolValue(true), types.BoolValue(false), types.StringValue("disabled"), true},
		{"disabling", types.BoolValue(false), types.BoolValue(true), types.StringValue("active"), true},
		{"unchanged active", types.BoolValue(true), types.BoolValue(true), types.StringValue("active"), false},
		{"unchanged disabled", types.BoolValue(false), types.BoolValue(false), types.StringValue("disabled"), false},
		{"planned unknown", types.BoolUnknown(), types.BoolValue(false), types.StringValue("disabled"), true},
		{"planned null", types.BoolNull(), types.BoolValue(false), types.StringValue("disabled"), true},
		// State left inconsistent by an apply whose activation errored — this
		// apply can repair it, so status must be planned as unknown.
		{"state disagrees with recorded activation", types.BoolValue(true), types.BoolValue(true), types.StringValue("disabled"), true},
		// A data flow whose API response never carries river_status keeps a null
		// status. Planning it as unknown would show a no-op update every plan.
		{"status never reported", types.BoolValue(true), types.BoolValue(true), types.StringNull(), false},
		{"status unknown in state", types.BoolValue(false), types.BoolValue(false), types.StringUnknown(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusMayChange(tc.planActivate, tc.stateActivate, tc.stateStatus); got != tc.want {
				t.Errorf("statusMayChange() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestActivationStatusModifier(t *testing.T) {
	ctx := context.Background()
	s := dataFlowSchemaForTest(t)

	cases := []struct {
		name          string
		nullState     bool
		nullPlan      bool
		planActivate  types.Bool
		stateActivate types.Bool
		stateStatus   types.String
		plannedStatus types.String
		want          types.String
	}{
		{
			name: "create leaves the framework's unknown alone", nullState: true,
			planActivate: types.BoolValue(true), plannedStatus: types.StringUnknown(),
			want: types.StringUnknown(),
		},
		{
			name: "destroy is untouched", nullPlan: true,
			planActivate: types.BoolNull(), stateActivate: types.BoolValue(true),
			stateStatus: types.StringValue("active"), plannedStatus: types.StringValue("active"),
			want: types.StringValue("active"),
		},
		{
			name:         "activating plans status as known after apply",
			planActivate: types.BoolValue(true), stateActivate: types.BoolValue(false),
			stateStatus: types.StringValue("disabled"), plannedStatus: types.StringValue("disabled"),
			want: types.StringUnknown(),
		},
		{
			name:         "disabling plans status as known after apply",
			planActivate: types.BoolValue(false), stateActivate: types.BoolValue(true),
			stateStatus: types.StringValue("active"), plannedStatus: types.StringValue("active"),
			want: types.StringUnknown(),
		},
		{
			name:         "no activation change keeps the prior status",
			planActivate: types.BoolValue(true), stateActivate: types.BoolValue(true),
			stateStatus: types.StringValue("active"), plannedStatus: types.StringValue("active"),
			want: types.StringValue("active"),
		},
		{
			// Terraform plans a null Computed attribute as unknown; re-planning it
			// null is what keeps flows without river_status from showing a no-op
			// "update in place" on every plan.
			name:         "never-reported status stays null instead of unknown",
			planActivate: types.BoolValue(false), stateActivate: types.BoolValue(false),
			stateStatus: types.StringNull(), plannedStatus: types.StringUnknown(),
			want: types.StringNull(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			planRaw := dataFlowRawForTest(t, s, tc.planActivate, tc.plannedStatus)
			stateRaw := dataFlowRawForTest(t, s, tc.stateActivate, tc.stateStatus)
			if tc.nullState {
				stateRaw = tftypes.NewValue(planRaw.Type(), nil)
			}
			if tc.nullPlan {
				planRaw = tftypes.NewValue(stateRaw.Type(), nil)
			}
			req := planmodifier.StringRequest{
				Path:       path.Root("status"),
				Plan:       tfsdk.Plan{Raw: planRaw, Schema: s},
				State:      tfsdk.State{Raw: stateRaw, Schema: s},
				StateValue: tc.stateStatus,
				PlanValue:  tc.plannedStatus,
			}
			resp := &planmodifier.StringResponse{PlanValue: tc.plannedStatus}
			activationStatusModifier{}.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("plan modifier diagnostics: %v", resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Errorf("PlanValue = %v, want %v", resp.PlanValue, tc.want)
			}
		})
	}
}

// TestDataFlowActivateHasNoDefault pins the deliberate schema decision behind the
// activate semantics: an omitted activate means "activation is not managed", so
// refreshing the server's river_status into activate cannot make Terraform
// disable a data flow that something else (the console, data_flow_run) activated.
// A static default would turn every such refresh into a disable.
func TestDataFlowActivateHasNoDefault(t *testing.T) {
	s := dataFlowSchemaForTest(t)

	activate, ok := s.Attributes["activate"].(schema.BoolAttribute)
	if !ok {
		t.Fatalf("activate is %T, want schema.BoolAttribute", s.Attributes["activate"])
	}
	if activate.Default != nil {
		t.Errorf("activate must not declare a default: %T", activate.Default)
	}
	if !activate.Optional || !activate.Computed {
		t.Errorf("activate must be Optional+Computed, got Optional=%v Computed=%v", activate.Optional, activate.Computed)
	}

	status, ok := s.Attributes["status"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("status is %T, want schema.StringAttribute", s.Attributes["status"])
	}
	if status.Optional || status.Required || !status.Computed {
		t.Errorf("status must be Computed-only, got Optional=%v Required=%v Computed=%v",
			status.Optional, status.Required, status.Computed)
	}
	if len(status.PlanModifiers) != 1 {
		t.Fatalf("status must carry the activation plan modifier, got %d plan modifiers", len(status.PlanModifiers))
	}
	if _, ok := status.PlanModifiers[0].(activationStatusModifier); !ok {
		t.Errorf("status plan modifier is %T, want activationStatusModifier", status.PlanModifiers[0])
	}
}

// ── Create: CDC ordering, diagnostics, and no orphaned data flow ──────────────

const cdcProps = `{"properties_type":"source_to_target","source":{"additional_settings":{"extract_method":"log"}}}`
const batchProps = `{"properties_type":"source_to_target","source":{"additional_settings":{"extract_method":"incremental"}}}`

func TestDataFlowCreateCallSequence(t *testing.T) {
	cases := []struct {
		name           string
		props          string
		activate       types.Bool
		enableCDCFails bool
		wantCalls      []string
		wantErr        string
		wantStatus     types.String
		wantActivate   types.Bool
	}{
		{
			// The bug being fixed: a data flow that is CDC from its first apply
			// used to be activated without ever being CDC-enabled.
			name:  "cdc flow with activate true: POST, PUT, enable_cdc, activate",
			props: cdcProps, activate: types.BoolValue(true),
			wantCalls: []string{
				"POST /v1/accounts/acc/environments/env1/rivers",
				"GET /v1/accounts/acc/environments/env1/rivers/river1",
				"PUT /v1/accounts/acc/environments/env1/rivers/river1",
				"POST /v1/accounts/acc/environments/env1/rivers/river1/enable_cdc",
				"POST /v1/accounts/acc/environments/env1/rivers/river1/activate_river",
			},
			wantStatus: types.StringValue("active"), wantActivate: types.BoolValue(true),
		},
		{
			name:  "batch flow with activate true skips enable_cdc",
			props: batchProps, activate: types.BoolValue(true),
			wantCalls: []string{
				"POST /v1/accounts/acc/environments/env1/rivers",
				"GET /v1/accounts/acc/environments/env1/rivers/river1",
				"PUT /v1/accounts/acc/environments/env1/rivers/river1",
				"POST /v1/accounts/acc/environments/env1/rivers/river1/activate_river",
			},
			wantStatus: types.StringValue("active"), wantActivate: types.BoolValue(true),
		},
		{
			name:  "activate false only creates, and reports the server status",
			props: cdcProps, activate: types.BoolValue(false),
			wantCalls:  []string{"POST /v1/accounts/acc/environments/env1/rivers"},
			wantStatus: types.StringValue("disabled"), wantActivate: types.BoolValue(false),
		},
		{
			// activate omitted from the configuration: no default, nothing is
			// activated, and state records what actually happened.
			name:  "activate omitted leaves the flow alone",
			props: cdcProps, activate: types.BoolUnknown(),
			wantCalls:  []string{"POST /v1/accounts/acc/environments/env1/rivers"},
			wantStatus: types.StringValue("disabled"), wantActivate: types.BoolValue(false),
		},
		{
			// A failing enable_cdc must surface a diagnostic and must NOT go on to
			// activate — while still recording the created data flow in state, or
			// the next apply creates a duplicate.
			name:  "enable_cdc failure is reported and stops before activation",
			props: cdcProps, activate: types.BoolValue(true), enableCDCFails: true,
			wantCalls: []string{
				"POST /v1/accounts/acc/environments/env1/rivers",
				"GET /v1/accounts/acc/environments/env1/rivers/river1",
				"PUT /v1/accounts/acc/environments/env1/rivers/river1",
				"POST /v1/accounts/acc/environments/env1/rivers/river1/enable_cdc",
			},
			wantErr:    "Error enabling CDC for data flow",
			wantStatus: types.StringValue("disabled"), wantActivate: types.BoolValue(true),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls = append(calls, req.Method+" "+req.URL.Path)
				switch {
				case strings.HasSuffix(req.URL.Path, "/enable_cdc") && tc.enableCDCFails:
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"detail":"Error in connecting to the source"}`))
				case strings.HasSuffix(req.URL.Path, "/enable_cdc"),
					strings.HasSuffix(req.URL.Path, "/activate_river"):
					// 204: the operation completed synchronously, nothing to poll.
					w.WriteHeader(http.StatusNoContent)
				default:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"cross_id":"river1","name":"flow","kind":"main_river",` +
						`"type":"source_to_target","metadata":{"river_status":"disabled"}}`))
				}
			}))
			defer srv.Close()

			c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
			if err != nil {
				t.Fatalf("client.New: %v", err)
			}
			r := &dataFlowResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
			s := dataFlowSchemaForTest(t)

			planRaw := dataFlowCreatePlanForTest(t, s, tc.props, tc.activate)
			resp := &resource.CreateResponse{
				State: tfsdk.State{Raw: tftypes.NewValue(planRaw.Type(), nil), Schema: s},
			}
			r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: planRaw, Schema: s}}, resp)

			if diff := diffStrings(calls, tc.wantCalls); diff != "" {
				t.Errorf("API call sequence mismatch:\n%s", diff)
			}
			if tc.wantErr == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
				}
			} else {
				if !resp.Diagnostics.HasError() {
					t.Fatalf("expected an error diagnostic containing %q, got none", tc.wantErr)
				}
				if got := resp.Diagnostics.Errors()[0].Summary(); !strings.Contains(got, tc.wantErr) {
					t.Errorf("error summary = %q, want it to contain %q", got, tc.wantErr)
				}
			}

			// State must always be written once the POST succeeded, error or not.
			var id types.String
			resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
			if id.ValueString() != "river1" {
				t.Errorf("state id = %v, want \"river1\" (a data flow left out of state is created twice)", id)
			}
			var gotStatus types.String
			resp.State.GetAttribute(ctx, path.Root("status"), &gotStatus)
			if !gotStatus.Equal(tc.wantStatus) {
				t.Errorf("state status = %v, want %v", gotStatus, tc.wantStatus)
			}
			var gotActivate types.Bool
			resp.State.GetAttribute(ctx, path.Root("activate"), &gotActivate)
			if !gotActivate.Equal(tc.wantActivate) {
				t.Errorf("state activate = %v, want %v", gotActivate, tc.wantActivate)
			}
		})
	}
}

// TestDataFlowUpdateDisablesOnActivateFalse covers the other half of the
// activate state machine: with activate flipped true -> false the flow is
// actually disabled (and not re-activated), so the drift Read reported
// converges instead of re-planning forever.
func TestDataFlowUpdateActivationTransitions(t *testing.T) {
	cases := []struct {
		name          string
		props         string
		planActivate  types.Bool
		stateActivate bool
		wantCalls     []string
		wantStatus    types.String
	}{
		{
			name:  "true -> false disables and does not re-activate",
			props: batchProps, planActivate: types.BoolValue(false), stateActivate: true,
			wantCalls: []string{
				"POST /v1/accounts/acc/environments/env1/rivers/river1/disable_river",
				"GET /v1/accounts/acc/environments/env1/rivers/river1",
				"PUT /v1/accounts/acc/environments/env1/rivers/river1",
			},
			wantStatus: types.StringValue("disabled"),
		},
		{
			// Activating a data flow that is already CDC does not go through the
			// switch-to-CDC path, so enable_cdc has to be driven from here too —
			// otherwise "create disabled, activate later" silently skips it.
			// No disable_river here: the flow is already inactive (wasActive=false),
			// so there's nothing to deactivate — disable_river is only for
			// realizing a true -> false transition, never a defensive pre-step
			// (CORE-2346: PUT has no general "must be inactive" requirement).
			name:  "false -> true on a CDC flow enables CDC before activating",
			props: cdcProps, planActivate: types.BoolValue(true), stateActivate: false,
			wantCalls: []string{
				"GET /v1/accounts/acc/environments/env1/rivers/river1",
				"PUT /v1/accounts/acc/environments/env1/rivers/river1",
				"POST /v1/accounts/acc/environments/env1/rivers/river1/enable_cdc",
				"POST /v1/accounts/acc/environments/env1/rivers/river1/activate_river",
			},
			wantStatus: types.StringValue("active"),
		},
		{
			name:  "unchanged and inactive touches no activation endpoint",
			props: batchProps, planActivate: types.BoolValue(false), stateActivate: false,
			wantCalls: []string{
				"GET /v1/accounts/acc/environments/env1/rivers/river1",
				"PUT /v1/accounts/acc/environments/env1/rivers/river1",
			},
			wantStatus: types.StringValue("disabled"),
		},
		{
			// The case this whole fix is about: editing an already-active flow's
			// content, with activate staying true, must not disable it first —
			// that was pure defensive overhead the real API never required.
			name:  "unchanged and active (a content edit) touches no disable endpoint",
			props: batchProps, planActivate: types.BoolValue(true), stateActivate: true,
			wantCalls: []string{
				"GET /v1/accounts/acc/environments/env1/rivers/river1",
				"PUT /v1/accounts/acc/environments/env1/rivers/river1",
				"POST /v1/accounts/acc/environments/env1/rivers/river1/activate_river",
			},
			wantStatus: types.StringValue("active"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls = append(calls, req.Method+" "+req.URL.Path)
				switch {
				case strings.HasSuffix(req.URL.Path, "/disable_river"),
					strings.HasSuffix(req.URL.Path, "/enable_cdc"),
					strings.HasSuffix(req.URL.Path, "/activate_river"):
					w.WriteHeader(http.StatusNoContent)
				default:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"cross_id":"river1","name":"flow","kind":"main_river",` +
						`"type":"source_to_target","metadata":{"river_status":"disabled"}}`))
				}
			}))
			defer srv.Close()

			c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
			if err != nil {
				t.Fatalf("client.New: %v", err)
			}
			r := &dataFlowResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
			s := dataFlowSchemaForTest(t)

			// status is planned as unknown whenever activation changes (see the
			// plan modifier), and kept otherwise.
			plannedStatus := types.StringUnknown()
			priorStatus := types.StringValue(riverStatusDisabled)
			if tc.stateActivate {
				priorStatus = types.StringValue(riverStatusActive)
			}
			if !statusMayChange(tc.planActivate, types.BoolValue(tc.stateActivate), priorStatus) {
				plannedStatus = priorStatus
			}

			planRaw := dataFlowUpdatePlanForTest(t, s, tc.props, tc.planActivate, plannedStatus)
			stateRaw := dataFlowUpdatePlanForTest(t, s, tc.props, types.BoolValue(tc.stateActivate), priorStatus)
			resp := &resource.UpdateResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
			r.Update(ctx, resource.UpdateRequest{
				Plan:  tfsdk.Plan{Raw: planRaw, Schema: s},
				State: tfsdk.State{Raw: stateRaw, Schema: s},
			}, resp)

			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			if diff := diffStrings(calls, tc.wantCalls); diff != "" {
				t.Errorf("API call sequence mismatch:\n%s", diff)
			}
			var gotStatus types.String
			resp.State.GetAttribute(ctx, path.Root("status"), &gotStatus)
			if !gotStatus.Equal(tc.wantStatus) {
				t.Errorf("state status = %v, want %v", gotStatus, tc.wantStatus)
			}
			var gotActivate types.Bool
			resp.State.GetAttribute(ctx, path.Root("activate"), &gotActivate)
			if !gotActivate.Equal(tc.planActivate) {
				t.Errorf("state activate = %v, want the planned %v", gotActivate, tc.planActivate)
			}
		})
	}
}

// TestDataFlowUpdateSkipsDisableForLogicType proves the CORE-2346 fix: the
// real API rejects disable_river outright for type="logic" (400:
// "disable_river is not supported for logic river types") because logic
// rivers are hardcoded server-side to always be ACTIVE — there is no
// DISABLED state for this type to transition into. Every OTHER type still
// calls disable_river for a genuine active -> inactive request (see
// TestDataFlowUpdateActivationTransitions's "true -> false" case); logic
// never does, not even then, since that call would always 400.
func TestDataFlowUpdateSkipsDisableForLogicType(t *testing.T) {
	const logicProps = `{"properties_type":"logic","logic_steps":[]}`

	cases := []struct {
		name          string
		planActivate  bool
		stateActivate bool
	}{
		// The case that unconditionally hit disable_river pre-fix (the
		// original guard was `wantActive || wasActive`, not gated on an
		// actual activate transition): a content edit with activate
		// staying true.
		{"active staying active (a content edit)", true, true},
		// The one case that's STILL special about logic even after the
		// general fix (CORE-2346): every other type calls disable_river
		// for a genuine active -> inactive request (see
		// TestDataFlowUpdateActivationTransitions's "true -> false"), but
		// logic never does — that call would always 400, since logic
		// rivers have no DISABLED state to transition into.
		{"attempted true -> false", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			var calls []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls = append(calls, req.Method+" "+req.URL.Path)
				switch {
				case strings.HasSuffix(req.URL.Path, "/disable_river"):
					// Real behavior being guarded against: this must never be
					// called for type="logic". If it is, fail the way the real
					// API does.
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"detail":"disable_river is not supported for logic river types."}`))
				case strings.HasSuffix(req.URL.Path, "/activate_river"):
					w.WriteHeader(http.StatusNoContent)
				default:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"cross_id":"river1","name":"flow","kind":"main_river",` +
						`"type":"logic","metadata":{"river_status":"active"}}`))
				}
			}))
			defer srv.Close()

			c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
			if err != nil {
				t.Fatalf("client.New: %v", err)
			}
			r := &dataFlowResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
			s := dataFlowSchemaForTest(t)

			build := func(activate bool) tftypes.Value {
				return dataFlowObjectForTest(t, s, map[string]tftypes.Value{
					"id":              tftypes.NewValue(tftypes.String, "river1"),
					"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
					"name":            tftypes.NewValue(tftypes.String, "flow"),
					"kind":            tftypes.NewValue(tftypes.String, "main_river"),
					"type":            tftypes.NewValue(tftypes.String, "logic"),
					"properties_json": tftypes.NewValue(tftypes.String, logicProps),
					"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
					"schedulers_json": tftypes.NewValue(tftypes.String, `[{"cron_expression":"0 * * * *","is_enabled":true}]`),
					"activate":        boolRaw(types.BoolValue(activate)),
					"status":          stringRaw(types.StringValue(riverStatusActive)),
					"step_ids":        tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
				})
			}
			planRaw := build(tc.planActivate)
			stateRaw := build(tc.stateActivate)

			resp := &resource.UpdateResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
			r.Update(ctx, resource.UpdateRequest{
				Plan:  tfsdk.Plan{Raw: planRaw, Schema: s},
				State: tfsdk.State{Raw: stateRaw, Schema: s},
			}, resp)

			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			for _, call := range calls {
				if strings.Contains(call, "disable_river") {
					t.Errorf("disable_river must never be called for type=logic; got calls: %v", calls)
				}
			}
		})
	}
}

// TestDataFlowUpdateDisablesCDCBeforePropertiesChange proves the narrow
// exception CORE-2346 identified: a CDC (log-based) river with CDC logging
// currently enabled rejects a `properties` change outright with a 400
// ("can not update properties for an active data flow. Please disable the
// data flow and try again") — a real, unconditional server-side lock.
//
// The name of that error is misleading: the guard's real predicate is
// is_river_cdc_enabled(...), which reads shared_params.enable_log -- a field
// entirely independent of river_status/activate. disable_river never
// touches it (confirmed against the backend source), so calling
// disable_river/retrying alone does not reliably clear it. disable_cdc is
// what actually clears it, and this is called proactively (not reactively on
// the error) because, unlike state.Activate, extract_method is structural --
// not something our own activate/deactivate calls flip mid-run the way
// river_status is, so there's no plan-time-vs-apply-time staleness risk here
// (see TestDataFlowUpdateSkipsDisableForLogicType's sibling test file
// history for that staleness story on the *river_status* side).
func TestDataFlowUpdateDisablesCDCBeforePropertiesChange(t *testing.T) {
	ctx := context.Background()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch {
		case strings.HasSuffix(req.URL.Path, "/disable_cdc"),
			strings.HasSuffix(req.URL.Path, "/enable_cdc"),
			strings.HasSuffix(req.URL.Path, "/activate_river"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(req.URL.Path, "/disable_river"):
			t.Errorf("disable_river does not clear enable_log and should not be called for this")
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cross_id":"river1","name":"flow","kind":"main_river",` +
				`"type":"source_to_target","metadata":{"river_status":"active"}}`))
		}
	}))
	defer srv.Close()

	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	r := &dataFlowResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
	s := dataFlowSchemaForTest(t)

	const cdcPropsV2 = `{"properties_type":"source_to_target","source":{"additional_settings":{"extract_method":"log","cdc_override":{"include_snapshot_tables":false}}}}`
	build := func(props string) tftypes.Value {
		return dataFlowObjectForTest(t, s, map[string]tftypes.Value{
			"id":              tftypes.NewValue(tftypes.String, "river1"),
			"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
			"name":            tftypes.NewValue(tftypes.String, "flow"),
			"kind":            tftypes.NewValue(tftypes.String, "main_river"),
			"type":            tftypes.NewValue(tftypes.String, "source_to_target"),
			"properties_json": tftypes.NewValue(tftypes.String, props),
			"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
			"schedulers_json": tftypes.NewValue(tftypes.String, `[{"cron_expression":"0 * * * *","is_enabled":true}]`),
			"activate":        boolRaw(types.BoolValue(true)),
			"status":          stringRaw(types.StringValue(riverStatusActive)),
			"step_ids":        tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
		})
	}
	planRaw := build(cdcPropsV2)
	stateRaw := build(cdcProps)

	resp := &resource.UpdateResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	r.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Raw: planRaw, Schema: s},
		State: tfsdk.State{Raw: stateRaw, Schema: s},
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	wantCalls := []string{
		"POST /v1/accounts/acc/environments/env1/rivers/river1/disable_cdc",
		"GET /v1/accounts/acc/environments/env1/rivers/river1",
		"PUT /v1/accounts/acc/environments/env1/rivers/river1",
		"POST /v1/accounts/acc/environments/env1/rivers/river1/enable_cdc",
		"POST /v1/accounts/acc/environments/env1/rivers/river1/activate_river",
	}
	if diff := diffStrings(calls, wantCalls); diff != "" {
		t.Errorf("API call sequence mismatch:\n%s", diff)
	}
}

// TestDataFlowUpdateSkipsCDCDisableWhenPropertiesUnchanged proves the other
// half of the same fix: an update that doesn't touch `properties` at all
// (e.g. renaming the flow) never calls disable_cdc — no unnecessary
// disable/re-enable round trip interrupting CDC capture for no reason.
func TestDataFlowUpdateSkipsCDCDisableWhenPropertiesUnchanged(t *testing.T) {
	ctx := context.Background()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch {
		case strings.HasSuffix(req.URL.Path, "/disable_cdc"):
			t.Errorf("disable_cdc must not be called when properties is unchanged")
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(req.URL.Path, "/enable_cdc"),
			strings.HasSuffix(req.URL.Path, "/activate_river"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cross_id":"river1","name":"flow","kind":"main_river",` +
				`"type":"source_to_target","metadata":{"river_status":"active"}}`))
		}
	}))
	defer srv.Close()

	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	r := &dataFlowResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
	s := dataFlowSchemaForTest(t)

	build := func(name string) tftypes.Value {
		return dataFlowObjectForTest(t, s, map[string]tftypes.Value{
			"id":              tftypes.NewValue(tftypes.String, "river1"),
			"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
			"name":            tftypes.NewValue(tftypes.String, name),
			"kind":            tftypes.NewValue(tftypes.String, "main_river"),
			"type":            tftypes.NewValue(tftypes.String, "source_to_target"),
			"properties_json": tftypes.NewValue(tftypes.String, cdcProps),
			"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
			"schedulers_json": tftypes.NewValue(tftypes.String, `[{"cron_expression":"0 * * * *","is_enabled":true}]`),
			"activate":        boolRaw(types.BoolValue(true)),
			"status":          stringRaw(types.StringValue(riverStatusActive)),
			"step_ids":        tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
		})
	}
	planRaw := build("flow renamed")
	stateRaw := build("flow")

	resp := &resource.UpdateResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	r.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Raw: planRaw, Schema: s},
		State: tfsdk.State{Raw: stateRaw, Schema: s},
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	for _, call := range calls {
		if strings.Contains(call, "disable_cdc") {
			t.Errorf("disable_cdc must not be called when properties is unchanged; got calls: %v", calls)
		}
	}
}

// TestDataFlowReadReconcilesActivate proves the refresh side of the state
// machine: river_status lands on both status and activate, and an absent
// river_status changes neither.
func TestDataFlowReadReconcilesActivate(t *testing.T) {
	cases := []struct {
		name         string
		metadata     string
		priorActive  bool
		wantActivate types.Bool
		wantStatus   types.String
	}{
		{"activated out of band", `{"river_status":"active"}`, false, types.BoolValue(true), types.StringValue("active")},
		{"disabled out of band", `{"river_status":"disabled"}`, true, types.BoolValue(false), types.StringValue("disabled")},
		{"no change", `{"river_status":"active"}`, true, types.BoolValue(true), types.StringValue("active")},
		// Measured on real GET bodies: a small fraction carry no river_status.
		{"river_status absent keeps the prior activate", `{"description":"x"}`, true, types.BoolValue(true), types.StringNull()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"cross_id":"river1","name":"flow","kind":"main_river",` +
					`"type":"source_to_target","metadata":` + tc.metadata + `}`))
			}))
			defer srv.Close()

			c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
			if err != nil {
				t.Fatalf("client.New: %v", err)
			}
			r := &dataFlowResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
			s := dataFlowSchemaForTest(t)

			priorStatus := types.StringValue(riverStatusDisabled)
			if tc.priorActive {
				priorStatus = types.StringValue(riverStatusActive)
			}
			stateRaw := dataFlowUpdatePlanForTest(t, s, batchProps, types.BoolValue(tc.priorActive), priorStatus)
			resp := &resource.ReadResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
			r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Raw: stateRaw, Schema: s}}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}

			var gotActivate types.Bool
			resp.State.GetAttribute(ctx, path.Root("activate"), &gotActivate)
			if !gotActivate.Equal(tc.wantActivate) {
				t.Errorf("refreshed activate = %v, want %v", gotActivate, tc.wantActivate)
			}
			var gotStatus types.String
			resp.State.GetAttribute(ctx, path.Root("status"), &gotStatus)
			if !gotStatus.Equal(tc.wantStatus) {
				t.Errorf("refreshed status = %v, want %v", gotStatus, tc.wantStatus)
			}
		})
	}
}

// ── test helpers ──────────────────────────────────────────────────────────────

func dataFlowSchemaForTest(t *testing.T) schema.Schema {
	t.Helper()
	resp := &resource.SchemaResponse{}
	NewDataFlowResource().Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("data flow schema diagnostics: %v", resp.Diagnostics)
	}
	return resp.Schema
}

// dataFlowObjectForTest builds a full resource object with every attribute null,
// then applies the given overrides — so a test only has to name the attributes
// it cares about.
func dataFlowObjectForTest(t *testing.T, s schema.Schema, overrides map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	objType, ok := s.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatalf("data flow schema type is not an object")
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

func boolRaw(v types.Bool) tftypes.Value {
	switch {
	case v.IsUnknown():
		return tftypes.NewValue(tftypes.Bool, tftypes.UnknownValue)
	case v.IsNull():
		return tftypes.NewValue(tftypes.Bool, nil)
	default:
		return tftypes.NewValue(tftypes.Bool, v.ValueBool())
	}
}

func stringRaw(v types.String) tftypes.Value {
	switch {
	case v.IsUnknown():
		return tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	case v.IsNull():
		return tftypes.NewValue(tftypes.String, nil)
	default:
		return tftypes.NewValue(tftypes.String, v.ValueString())
	}
}

func dataFlowRawForTest(t *testing.T, s schema.Schema, activate types.Bool, status types.String) tftypes.Value {
	t.Helper()
	return dataFlowObjectForTest(t, s, map[string]tftypes.Value{
		"activate": boolRaw(activate),
		"status":   stringRaw(status),
	})
}

// dataFlowCreatePlanForTest mirrors the plan Terraform produces for a create:
// configured values known, computed values unknown.
func dataFlowCreatePlanForTest(t *testing.T, s schema.Schema, props string, activate types.Bool) tftypes.Value {
	t.Helper()
	return dataFlowObjectForTest(t, s, map[string]tftypes.Value{
		"id":              tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
		"name":            tftypes.NewValue(tftypes.String, "flow"),
		"kind":            tftypes.NewValue(tftypes.String, "main_river"),
		"type":            tftypes.NewValue(tftypes.String, "source_to_target"),
		"properties_json": tftypes.NewValue(tftypes.String, props),
		"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
		"schedulers_json": tftypes.NewValue(tftypes.String, `[{"cron_expression":"0 * * * *","is_enabled":true}]`),
		"group_id":        tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"activate":        boolRaw(activate),
		"status":          tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"step_ids":        tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, tftypes.UnknownValue),
	})
}

func dataFlowUpdatePlanForTest(
	t *testing.T, s schema.Schema, props string, activate types.Bool, status types.String,
) tftypes.Value {
	t.Helper()
	return dataFlowObjectForTest(t, s, map[string]tftypes.Value{
		"id":              tftypes.NewValue(tftypes.String, "river1"),
		"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
		"name":            tftypes.NewValue(tftypes.String, "flow"),
		"kind":            tftypes.NewValue(tftypes.String, "main_river"),
		"type":            tftypes.NewValue(tftypes.String, "source_to_target"),
		"properties_json": tftypes.NewValue(tftypes.String, props),
		"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
		"schedulers_json": tftypes.NewValue(tftypes.String, `[{"cron_expression":"0 * * * *","is_enabled":true}]`),
		"activate":        boolRaw(activate),
		"status":          stringRaw(status),
		"step_ids":        tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
	})
}

// diffStrings renders a readable mismatch between two call sequences.
func diffStrings(got, want []string) string {
	if len(got) == len(want) {
		same := true
		for i := range got {
			if got[i] != want[i] {
				same = false
				break
			}
		}
		if same {
			return ""
		}
	}
	g, _ := json.MarshalIndent(got, "", "  ")
	w, _ := json.MarshalIndent(want, "", "  ")
	return "got:  " + string(g) + "\nwant: " + string(w)
}

// ── typed settings / schedule tests ───────────────────────────────────────────
// TestDataFlowSchemaTypedBlocks asserts the typed blocks exist alongside — not
// instead of — the deprecated JSON attributes, and that the JSON ones now carry
// a deprecation message.
func TestDataFlowSchemaTypedBlocks(t *testing.T) {
	resp := &resource.SchemaResponse{}
	NewDataFlowResource().Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	settings, ok := resp.Schema.Attributes["settings"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("settings is not a single nested attribute: %T", resp.Schema.Attributes["settings"])
	}
	notification, ok := settings.Attributes["notification"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("settings.notification is not a single nested attribute: %T", settings.Attributes["notification"])
	}
	// RiverSettings has exactly two fields; NotificationSettings exactly three.
	if len(settings.Attributes) != 2 {
		t.Errorf("settings should mirror RiverSettings' 2 fields, got %d", len(settings.Attributes))
	}
	for _, want := range []string{"warning", "failure", "run_threshold"} {
		report, ok := notification.Attributes[want].(schema.SingleNestedAttribute)
		if !ok {
			t.Fatalf("notification.%s missing or wrong type", want)
		}
		if email, ok := report.Attributes["email"].(schema.StringAttribute); !ok || !email.Required {
			t.Errorf("notification.%s.email must be a required string (API marks it required)", want)
		}
	}

	sched, ok := resp.Schema.Attributes["schedule"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("schedule is not a single nested attribute: %T", resp.Schema.Attributes["schedule"])
	}
	if len(sched.Attributes) != 2 {
		t.Errorf("schedule should mirror RiverSchedule's 2 fields, got %d", len(sched.Attributes))
	}

	for _, name := range []string{"settings_json", "schedulers_json"} {
		attr, ok := resp.Schema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s must remain a string attribute (non-breaking)", name)
		}
		if attr.DeprecationMessage == "" {
			t.Errorf("%s should carry a DeprecationMessage", name)
		}
	}
}

// baseDataFlowModel is a minimal valid plan: the only field buildBody strictly
// needs is a parseable properties_json object.
func baseDataFlowModel() dataFlowModel {
	return dataFlowModel{
		Name:           types.StringValue("flow"),
		Kind:           types.StringValue("main_river"),
		Type:           types.StringValue("source_to_target"),
		PropertiesJSON: jsontypes.NewNormalizedValue(`{"properties_type":"source_to_target"}`),
		SettingsJSON:   jsontypes.NewNormalizedNull(),
		SchedulersJSON: jsontypes.NewNormalizedNull(),
	}
}

// mustJSON renders a value the way the client will put it on the wire, so the
// assertions below compare the actual API write payload (Go map key order is
// normalised by encoding/json).
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	return string(raw)
}

func buildBodyOrFail(t *testing.T, plan dataFlowModel) map[string]any {
	t.Helper()
	var diags diag.Diagnostics
	body, ok := (&dataFlowResource{}).buildBody(plan, nil, &diags)
	if !ok || diags.HasError() {
		t.Fatalf("buildBody failed: %v", diags)
	}
	return body
}

// ── typed settings → API write body ───────────────────────────────────────────

func TestBuildBodyTypedSettingsFullRoundTrip(t *testing.T) {
	plan := baseDataFlowModel()
	plan.Settings = &dataFlowSettingsModel{
		RunTimeoutSeconds: types.Int64Value(43200),
		Notification: &dataFlowNotificationModel{
			Warning: &dataFlowNotificationReportModel{
				Email:     types.StringValue("warn@example.com"),
				IsEnabled: types.BoolValue(false),
			},
			Failure: &dataFlowNotificationReportModel{
				Email:     types.StringValue("ops@example.com"),
				IsEnabled: types.BoolValue(true),
			},
			RunThreshold: &dataFlowNotificationReportModel{
				Email:                     types.StringValue("ops@example.com"),
				IsEnabled:                 types.BoolValue(true),
				ExecutionTimeLimitSeconds: types.Int64Value(3600),
			},
		},
	}

	body := buildBodyOrFail(t, plan)
	got := mustJSON(t, body["settings"])
	want := `{"notification":{"failure":{"email":"ops@example.com","is_enabled":true},` +
		`"run_threshold":{"email":"ops@example.com","execution_time_limit_seconds":3600,"is_enabled":true},` +
		`"warning":{"email":"warn@example.com","is_enabled":false}},"run_timeout_seconds":43200}`
	if got != want {
		t.Fatalf("settings body mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildBodyTypedSettingsOmitsUnsetFields(t *testing.T) {
	// run_timeout_seconds unset must NOT be sent as an explicit null — the API
	// treats a missing value as "automatic timeout calculation", and the update
	// path strips nulls anyway.
	plan := baseDataFlowModel()
	plan.Settings = &dataFlowSettingsModel{
		RunTimeoutSeconds: types.Int64Null(),
		Notification:      nil,
	}

	if got := mustJSON(t, buildBodyOrFail(t, plan)["settings"]); got != `{}` {
		t.Fatalf("expected empty settings object, got %s", got)
	}

	// A notification block with no report set collapses to no notification key.
	plan.Settings.Notification = &dataFlowNotificationModel{}
	if got := mustJSON(t, buildBodyOrFail(t, plan)["settings"]); got != `{}` {
		t.Fatalf("expected empty settings object for empty notification, got %s", got)
	}
}

// ── typed schedule → API write body ───────────────────────────────────────────

func TestBuildBodyTypedScheduleWrapsIntoSchedulersList(t *testing.T) {
	plan := baseDataFlowModel()
	plan.Schedule = &dataFlowScheduleModel{
		CronExpression: types.StringValue("0 * * * *"),
		IsEnabled:      types.BoolValue(true),
	}

	body := buildBodyOrFail(t, plan)
	got := mustJSON(t, body["schedulers"])
	want := `[{"cron_expression":"0 * * * *","is_enabled":true}]`
	if got != want {
		t.Fatalf("schedulers body mismatch\n got: %s\nwant: %s", got, want)
	}
}

// The CDC path in Update() keys off body["schedulers"] being present. The typed
// block must satisfy that check exactly as schedulers_json does.
func TestBuildBodyTypedScheduleSatisfiesCDCSchedulerCheck(t *testing.T) {
	plan := baseDataFlowModel()
	plan.PropertiesJSON = jsontypes.NewNormalizedValue(
		`{"properties_type":"source_to_target","source":{"additional_settings":{"extract_method":"log"}}}`)
	plan.Schedule = &dataFlowScheduleModel{
		CronExpression: types.StringValue("*/5 * * * *"),
		IsEnabled:      types.BoolValue(true),
	}

	if !isCDCFlow(plan.PropertiesJSON.ValueString()) {
		t.Fatal("expected a CDC flow")
	}
	body := buildBodyOrFail(t, plan)
	schedulers, ok := body["schedulers"].([]any)
	if !ok || len(schedulers) != 1 {
		t.Fatalf("expected exactly one scheduler, got %#v", body["schedulers"])
	}
	elem, ok := schedulers[0].(map[string]any)
	if !ok || elem["is_enabled"] != true {
		t.Fatalf("expected an enabled scheduler, got %#v", schedulers[0])
	}
}

func TestBuildBodyNoScheduleOmitsSchedulersKey(t *testing.T) {
	if _, present := buildBodyOrFail(t, baseDataFlowModel())["schedulers"]; present {
		t.Fatal("schedulers must be absent when neither schedule nor schedulers_json is set")
	}
}

// ── the deprecated JSON attributes must keep working unchanged ────────────────

func TestBuildBodyJSONAttributesStillWork(t *testing.T) {
	plan := baseDataFlowModel()
	plan.SettingsJSON = jsontypes.NewNormalizedValue(`{"run_timeout_seconds":180}`)
	plan.SchedulersJSON = jsontypes.NewNormalizedValue(`[{"cron_expression":"0 0 * * *","is_enabled":true}]`)

	body := buildBodyOrFail(t, plan)
	if got := mustJSON(t, body["settings"]); got != `{"run_timeout_seconds":180}` {
		t.Fatalf("settings_json body mismatch: %s", got)
	}
	if got := mustJSON(t, body["schedulers"]); got != `[{"cron_expression":"0 0 * * *","is_enabled":true}]` {
		t.Fatalf("schedulers_json body mismatch: %s", got)
	}
}

// When the typed block is set, the Computed "{}" default that settings_json
// carries must not clobber it.
func TestBuildBodyTypedSettingsWinsOverComputedDefault(t *testing.T) {
	plan := baseDataFlowModel()
	plan.SettingsJSON = jsontypes.NewNormalizedValue(`{}`) // the schema default
	plan.Settings = &dataFlowSettingsModel{RunTimeoutSeconds: types.Int64Value(900)}

	if got := mustJSON(t, buildBodyOrFail(t, plan)["settings"]); got != `{"run_timeout_seconds":900}` {
		t.Fatalf("typed settings did not win over the settings_json default: %s", got)
	}
}

// ── mutual exclusion ──────────────────────────────────────────────────────────

func TestValidateDataFlowExclusivitySettingsConflict(t *testing.T) {
	cfg := baseDataFlowModel()
	cfg.Settings = &dataFlowSettingsModel{RunTimeoutSeconds: types.Int64Value(60)}
	cfg.SettingsJSON = jsontypes.NewNormalizedValue(`{"run_timeout_seconds":60}`)

	diags := validateDataFlowExclusivity(cfg)
	if !diags.HasError() {
		t.Fatal("expected an error when both settings and settings_json are set")
	}
	if s := diags.Errors()[0].Summary(); s != "Conflicting configuration: settings and settings_json" {
		t.Fatalf("unexpected diagnostic summary: %q", s)
	}
}

func TestValidateDataFlowExclusivityScheduleConflict(t *testing.T) {
	cfg := baseDataFlowModel()
	cfg.Schedule = &dataFlowScheduleModel{IsEnabled: types.BoolValue(true)}
	cfg.SchedulersJSON = jsontypes.NewNormalizedValue(`[{"is_enabled":true}]`)

	diags := validateDataFlowExclusivity(cfg)
	if !diags.HasError() {
		t.Fatal("expected an error when both schedule and schedulers_json are set")
	}
	if s := diags.Errors()[0].Summary(); s != "Conflicting configuration: schedule and schedulers_json" {
		t.Fatalf("unexpected diagnostic summary: %q", s)
	}
}

func TestValidateDataFlowExclusivityAllowsEitherAlone(t *testing.T) {
	cases := map[string]dataFlowModel{
		"neither": baseDataFlowModel(),
		"typed only": func() dataFlowModel {
			m := baseDataFlowModel()
			m.Settings = &dataFlowSettingsModel{RunTimeoutSeconds: types.Int64Value(60)}
			m.Schedule = &dataFlowScheduleModel{IsEnabled: types.BoolValue(true)}
			return m
		}(),
		"json only": func() dataFlowModel {
			m := baseDataFlowModel()
			m.SettingsJSON = jsontypes.NewNormalizedValue(`{}`)
			m.SchedulersJSON = jsontypes.NewNormalizedValue(`[]`)
			return m
		}(),
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if diags := validateDataFlowExclusivity(cfg); diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
		})
	}
}
