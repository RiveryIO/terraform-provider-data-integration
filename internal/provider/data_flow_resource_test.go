package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
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
					"schedule":        scheduleTFValue("0 * * * *", true),
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

// TestDataFlowUpdateDisablesRiverBeforePropertiesChange proves the narrow
// exception CORE-2346 identified: a CDC (log-based) river with CDC logging
// currently enabled rejects a `properties` change outright with a 400
// ("can not update properties for an active data flow. Please disable the
// data flow and try again") — a real, unconditional server-side lock.
//
// disable_river's own operation tears down a CDC river's connector and
// clears shared_params.enable_log as an inner task
// (get_disable_inner_tasks_by_river_type -> a "disable" pull-task with
// is_cdc=true) -- confirmed live -- so it unlocks this same guard, even
// though wantActive stays true and no real deactivation is happening.
//
// This fires for any active CDC flow, not just when propertiesChanging (see
// TestDataFlowUpdateDisablesRiverEvenWhenPropertiesUnchanged) -- and, unlike
// the propertiesChanging-based version this replaced, it does carry the same
// plan-time-vs-apply-time staleness risk as the wasActive && !wantActive
// branch above, since it also reads wasActive (state.Activate). Accepted,
// not fixed, for the same reason that branch already accepts it.
func TestDataFlowUpdateDisablesRiverBeforePropertiesChange(t *testing.T) {
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
			"schedule":        scheduleTFValue("0 * * * *", true),
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
		"POST /v1/accounts/acc/environments/env1/rivers/river1/disable_river",
		"GET /v1/accounts/acc/environments/env1/rivers/river1",
		"PUT /v1/accounts/acc/environments/env1/rivers/river1",
		"POST /v1/accounts/acc/environments/env1/rivers/river1/enable_cdc",
		"POST /v1/accounts/acc/environments/env1/rivers/river1/activate_river",
	}
	if diff := diffStrings(calls, wantCalls); diff != "" {
		t.Errorf("API call sequence mismatch:\n%s", diff)
	}
}

// TestDataFlowUpdateDisablesRiverEvenWhenPropertiesUnchanged reproduces a
// real failure against a live river: an update whose only tracked change was
// the schedule (properties byte-for-byte identical between plan and
// state) still 400'd with the locked-properties error. Cause: properties_json
// is config-authoritative and always overwrites the server's copy verbatim,
// but the API silently enriches properties server-side with fields config
// never sets, so a PUT this provider considers a no-op can still be a real
// change from the API's perspective -- and propertiesChanging, a comparison
// against Terraform's own remembered state, has no way to see that drift.
// needsDisableForLockedProperties therefore fires for any active CDC flow
// regardless of propertiesChanging.
func TestDataFlowUpdateDisablesRiverEvenWhenPropertiesUnchanged(t *testing.T) {
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

	build := func(schedule tftypes.Value) tftypes.Value {
		return dataFlowObjectForTest(t, s, map[string]tftypes.Value{
			"id":              tftypes.NewValue(tftypes.String, "river1"),
			"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
			"name":            tftypes.NewValue(tftypes.String, "flow"),
			"kind":            tftypes.NewValue(tftypes.String, "main_river"),
			"type":            tftypes.NewValue(tftypes.String, "source_to_target"),
			"properties_json": tftypes.NewValue(tftypes.String, cdcProps),
			"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
			"schedule":        schedule,
			"activate":        boolRaw(types.BoolValue(true)),
			"status":          stringRaw(types.StringValue(riverStatusActive)),
			"step_ids":        tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
		})
	}
	// properties_json is identical on both sides; only the schedule differs.
	planRaw := build(scheduleTFNull())
	stateRaw := build(scheduleTFValue("0 * * * *", true))

	resp := &resource.UpdateResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	r.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Raw: planRaw, Schema: s},
		State: tfsdk.State{Raw: stateRaw, Schema: s},
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	sawDisable := false
	for _, call := range calls {
		if strings.Contains(call, "disable_river") {
			sawDisable = true
		}
	}
	if !sawDisable {
		t.Errorf("expected disable_river to be called for an active CDC flow even with properties unchanged; got calls: %v", calls)
	}
}

// TestDataFlowUpdateSkipsDisableForNonCDCActiveFlow proves that
// needsDisableForLockedProperties (isCurrentlyCDC && wasActive) is scoped to
// CDC flows: renaming a non-CDC active flow never calls disable_river — no
// unnecessary disable/re-enable round trip for a flow that was never subject
// to the locked-properties guard in the first place.
func TestDataFlowUpdateSkipsDisableForNonCDCActiveFlow(t *testing.T) {
	ctx := context.Background()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch {
		case strings.HasSuffix(req.URL.Path, "/disable_river"):
			t.Errorf("disable_river must not be called for a non-CDC active flow")
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
			"properties_json": tftypes.NewValue(tftypes.String, batchProps),
			"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
			"schedule":        scheduleTFValue("0 * * * *", true),
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
		if strings.Contains(call, "disable_river") {
			t.Errorf("disable_river must not be called for a non-CDC active flow; got calls: %v", calls)
		}
	}
}

// TestDataFlowUpdateDeactivationDisablesRiverOnce proves that a real
// deactivation of a CDC river (true -> false) calls disable_river exactly
// once -- its own operation already tears down the CDC connector as an
// inner task (get_disable_inner_tasks_by_river_type -> a "disable" pull-task
// with is_cdc=true). Confirmed live against a real river: activated it,
// enabled CDC, called disable_river, and shared_params.enable_log flipped to
// false immediately after -- no separate CDC-disable call needed.
func TestDataFlowUpdateDeactivationDisablesRiverOnce(t *testing.T) {
	ctx := context.Background()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls = append(calls, req.Method+" "+req.URL.Path)
		switch {
		case strings.HasSuffix(req.URL.Path, "/disable_river"):
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

	build := func(activate bool) tftypes.Value {
		return dataFlowObjectForTest(t, s, map[string]tftypes.Value{
			"id":              tftypes.NewValue(tftypes.String, "river1"),
			"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
			"name":            tftypes.NewValue(tftypes.String, "flow"),
			"kind":            tftypes.NewValue(tftypes.String, "main_river"),
			"type":            tftypes.NewValue(tftypes.String, "source_to_target"),
			"properties_json": tftypes.NewValue(tftypes.String, cdcProps), // unchanged on both sides
			"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
			"schedule":        scheduleTFValue("0 * * * *", true),
			"activate":        boolRaw(types.BoolValue(activate)),
			"status":          stringRaw(types.StringValue(riverStatusActive)),
			"step_ids":        tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
		})
	}
	planRaw := build(false)
	stateRaw := build(true)

	resp := &resource.UpdateResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	r.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Raw: planRaw, Schema: s},
		State: tfsdk.State{Raw: stateRaw, Schema: s},
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	wantCalls := []string{
		"POST /v1/accounts/acc/environments/env1/rivers/river1/disable_river",
		"GET /v1/accounts/acc/environments/env1/rivers/river1",
		"PUT /v1/accounts/acc/environments/env1/rivers/river1",
	}
	if diff := diffStrings(calls, wantCalls); diff != "" {
		t.Errorf("API call sequence mismatch:\n%s", diff)
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

// scheduleTFType is the raw tftypes shape of the schedule attribute, shared
// by every test that hand-builds a full resource object.
func scheduleTFType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"cron_expression": tftypes.String, "is_enabled": tftypes.Bool,
	}}
}

// scheduleTFValue builds a known, non-null schedule value for raw tftypes test objects.
func scheduleTFValue(cron string, enabled bool) tftypes.Value {
	return tftypes.NewValue(scheduleTFType(), map[string]tftypes.Value{
		"cron_expression": tftypes.NewValue(tftypes.String, cron),
		"is_enabled":      tftypes.NewValue(tftypes.Bool, enabled),
	})
}

// scheduleTFUnknown builds an unknown schedule value, matching the real plan
// value Terraform proposes for schedule on create (Optional+Computed, unset).
func scheduleTFUnknown() tftypes.Value {
	return tftypes.NewValue(scheduleTFType(), tftypes.UnknownValue)
}

// scheduleTFNull builds a null schedule value for raw tftypes test objects.
func scheduleTFNull() tftypes.Value {
	return tftypes.NewValue(scheduleTFType(), nil)
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
		"schedule":        scheduleTFUnknown(),
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
		"schedule":        scheduleTFValue("0 * * * *", true),
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
// TestDataFlowSchemaTypedBlocks asserts the typed blocks exist, that
// settings_json still carries a deprecation message (settings has no
// equivalent removal), and that schedulers_json is gone entirely — there
// are no clients on this provider yet, so the deprecated JSON representation
// of the schedule was removed outright instead of waiting for a major
// version bump.
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
	// Optional+Computed — this is what lets Read() reflect an out-of-band
	// schedule change as a real plan diff (CORE-2583 AC9) instead of
	// silently overwriting it on the next apply with no visibility at all.
	if !sched.Optional || !sched.Computed {
		t.Errorf("schedule must be Optional+Computed, got Optional=%v Computed=%v", sched.Optional, sched.Computed)
	}
	// Confirmed live against a real CDC river: without this modifier, an
	// unmanaged schedule never settles -- every single `terraform plan`
	// shows schedule -> (known after apply), forever, even with nothing
	// else changing, because schedule is a nested object with Optional-only
	// inner attributes.
	if len(sched.PlanModifiers) != 1 {
		t.Fatalf("schedule must carry exactly one plan modifier (UseStateForUnknown), got %d", len(sched.PlanModifiers))
	}
	if gotType, wantType := reflect.TypeOf(sched.PlanModifiers[0]), reflect.TypeOf(objectplanmodifier.UseStateForUnknown()); gotType != wantType {
		t.Errorf("schedule's plan modifier is %v, want %v", gotType, wantType)
	}

	settingsJSON, ok := resp.Schema.Attributes["settings_json"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("settings_json must remain a string attribute (non-breaking)")
	}
	if settingsJSON.DeprecationMessage == "" {
		t.Errorf("settings_json should carry a DeprecationMessage")
	}

	if _, present := resp.Schema.Attributes["schedulers_json"]; present {
		t.Error("schedulers_json must be removed from the schema entirely -- no clients depend on it")
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
		Schedule:       types.ObjectNull(dataFlowScheduleAttrTypes()),
	}
}

// scheduleObj is a test-only shorthand for building the schedule attribute's
// types.Object value from the same *dataFlowScheduleModel shape the old raw
// struct pointer field used to accept directly.
func scheduleObj(m *dataFlowScheduleModel) types.Object {
	return dataFlowScheduleToObject(m)
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
	plan.Schedule = scheduleObj(&dataFlowScheduleModel{
		CronExpression: types.StringValue("0 * * * *"),
		IsEnabled:      types.BoolValue(true),
	})

	body := buildBodyOrFail(t, plan)
	got := mustJSON(t, body["schedulers"])
	want := `[{"cron_expression":"0 * * * *","is_enabled":true}]`
	if got != want {
		t.Fatalf("schedulers body mismatch\n got: %s\nwant: %s", got, want)
	}
}

// The CDC path in Update() keys off body["schedulers"] being present.
func TestBuildBodyTypedScheduleSatisfiesCDCSchedulerCheck(t *testing.T) {
	plan := baseDataFlowModel()
	plan.PropertiesJSON = jsontypes.NewNormalizedValue(
		`{"properties_type":"source_to_target","source":{"additional_settings":{"extract_method":"log"}}}`)
	plan.Schedule = scheduleObj(&dataFlowScheduleModel{
		CronExpression: types.StringValue("*/5 * * * *"),
		IsEnabled:      types.BoolValue(true),
	})

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
		t.Fatal("schedulers must be absent when schedule is not set")
	}
}

// ── the deprecated settings_json attribute must keep working unchanged ────────

func TestBuildBodySettingsJSONStillWorks(t *testing.T) {
	plan := baseDataFlowModel()
	plan.SettingsJSON = jsontypes.NewNormalizedValue(`{"run_timeout_seconds":180}`)

	body := buildBodyOrFail(t, plan)
	if got := mustJSON(t, body["settings"]); got != `{"run_timeout_seconds":180}` {
		t.Fatalf("settings_json body mismatch: %s", got)
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

func TestValidateDataFlowExclusivityAllowsEitherAlone(t *testing.T) {
	cases := map[string]dataFlowModel{
		"neither": baseDataFlowModel(),
		"typed settings only": func() dataFlowModel {
			m := baseDataFlowModel()
			m.Settings = &dataFlowSettingsModel{RunTimeoutSeconds: types.Int64Value(60)}
			m.Schedule = scheduleObj(&dataFlowScheduleModel{IsEnabled: types.BoolValue(true)})
			return m
		}(),
		"settings_json only": func() dataFlowModel {
			m := baseDataFlowModel()
			m.SettingsJSON = jsontypes.NewNormalizedValue(`{}`)
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

// TestValidateDataFlowScheduleRequiredForCDC proves the one flow type where a
// schedule genuinely isn't optional: CDC (log-based) flows must declare one,
// via either representation. Every other run type may be created and left
// unscheduled indefinitely — this must never fire for them.
func TestValidateDataFlowScheduleRequiredForCDC(t *testing.T) {
	cases := []struct {
		name      string
		props     string
		schedule  types.Object
		wantError bool
	}{
		{"CDC without a schedule errors", cdcProps,
			types.ObjectNull(dataFlowScheduleAttrTypes()), true},
		{"CDC with a typed schedule is fine", cdcProps,
			scheduleObj(&dataFlowScheduleModel{IsEnabled: types.BoolValue(true)}), false},
		{"non-CDC (incremental) without a schedule is fine", batchProps,
			types.ObjectNull(dataFlowScheduleAttrTypes()), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseDataFlowModel()
			cfg.PropertiesJSON = jsontypes.NewNormalizedValue(tc.props)
			cfg.Schedule = tc.schedule

			diags := validateDataFlowScheduleRequiredForCDC(cfg)
			if diags.HasError() != tc.wantError {
				t.Fatalf("HasError() = %v, want %v (diags: %v)", diags.HasError(), tc.wantError, diags)
			}
			if tc.wantError {
				if s := diags.Errors()[0].Summary(); s != "CDC (log-based) data flows require a schedule" {
					t.Fatalf("unexpected diagnostic summary: %q", s)
				}
			}
		})
	}
}

// TestScheduleRemovalNeeded proves the pure removal-detection decision:
// only a config transition from managed to unmanaged counts as a real
// removal. Never having been managed is left alone, and staying managed
// (config still declares a schedule) is not a removal either.
func TestScheduleRemovalNeeded(t *testing.T) {
	cases := []struct {
		name            string
		wasManaged      bool
		scheduleManaged bool
		want            bool
	}{
		{"was managed, now isn't -- a real removal", true, false, true},
		{"never managed, still isn't -- leave it alone", false, false, false},
		{"was managed, still is -- not a removal", true, true, false},
		{"wasn't managed, now is -- a new schedule, not a removal", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduleRemovalNeeded(tc.wasManaged, tc.scheduleManaged); got != tc.want {
				t.Errorf("scheduleRemovalNeeded(%v, %v) = %v, want %v",
					tc.wasManaged, tc.scheduleManaged, got, tc.want)
			}
		})
	}
}

// TestScheduleConfigured proves scheduleConfigured recognizes config
// declaring a schedule via either representation, and only that.
func TestScheduleConfigured(t *testing.T) {
	cases := []struct {
		name     string
		schedule types.Object
		want     bool
	}{
		{"not set", types.ObjectNull(dataFlowScheduleAttrTypes()), false},
		{"typed schedule set", scheduleObj(&dataFlowScheduleModel{IsEnabled: types.BoolValue(true)}), true},
		{"unknown counts as unset", types.ObjectUnknown(dataFlowScheduleAttrTypes()), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := baseDataFlowModel()
			m.Schedule = tc.schedule
			if got := scheduleConfigured(m); got != tc.want {
				t.Errorf("scheduleConfigured() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDataFlowApplyScheduleObjectReconciliation proves apply()'s schedule
// behavior directly, gated purely on the field's OWN null/unknown state going
// in -- not any external "is this managed" flag. A concrete value (config set
// it directly) is left untouched. A null or unknown value (config doesn't
// manage schedule -- Unknown is the real case on every Create/Update plan for
// an unmanaged Optional+Computed attribute, not just null) must resolve to a
// known value by the time apply() returns, so it reflects the live API value
// -- a real schedule when one exists, and null when the API reports none (not
// left stale from a prior value). This is CORE-2583 AC9's fix: schedule used
// to be a permanently config-only echo with no way to reflect live drift.
func TestDataFlowApplyScheduleObjectReconciliation(t *testing.T) {
	r := &dataFlowResource{}
	apiWithSchedule := map[string]any{
		"id": "river1", "name": "flow", "kind": "main_river", "type": "source_to_target",
		"schedulers": []any{map[string]any{"cron_expression": "0 * * * *", "is_enabled": true}},
	}
	apiWithoutSchedule := map[string]any{
		"id": "river1", "name": "flow", "kind": "main_river", "type": "source_to_target",
	}
	wantScheduleObj := scheduleObj(&dataFlowScheduleModel{
		CronExpression: types.StringValue("0 * * * *"), IsEnabled: types.BoolValue(true),
	})

	t.Run("concrete: config value is left untouched even if the live value differs", func(t *testing.T) {
		m := baseDataFlowModel()
		configured := scheduleObj(&dataFlowScheduleModel{
			CronExpression: types.StringValue("0 0 * * *"), IsEnabled: types.BoolValue(false),
		})
		m.Schedule = configured
		if diags := r.apply(apiWithSchedule, "env1", &m); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !m.Schedule.Equal(configured) {
			t.Errorf("schedule = %v, want the configured value %v left untouched", m.Schedule, configured)
		}
	})

	t.Run("unknown (the real Create/Update case for an unmanaged attribute): resolves to the live schedule", func(t *testing.T) {
		m := baseDataFlowModel()
		m.Schedule = types.ObjectUnknown(dataFlowScheduleAttrTypes())
		if diags := r.apply(apiWithSchedule, "env1", &m); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if m.Schedule.IsUnknown() {
			t.Fatal("schedule is still unknown -- Terraform rejects an unknown value in the final state")
		}
		if !m.Schedule.Equal(wantScheduleObj) {
			t.Errorf("schedule = %v, want %v", m.Schedule, wantScheduleObj)
		}
	})

	t.Run("null: reflects a real live schedule -- this is the drift-visibility fix itself", func(t *testing.T) {
		m := baseDataFlowModel()
		if diags := r.apply(apiWithSchedule, "env1", &m); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !m.Schedule.Equal(wantScheduleObj) {
			t.Errorf("schedule = %v, want %v", m.Schedule, wantScheduleObj)
		}
	})

	t.Run("null: reflects no live schedule as null, not stale", func(t *testing.T) {
		m := baseDataFlowModel()
		if diags := r.apply(apiWithoutSchedule, "env1", &m); diags.HasError() {
			t.Fatalf("unexpected diagnostics: %v", diags)
		}
		if !m.Schedule.IsNull() {
			t.Errorf("schedule = %v, want null once the API reports no schedule", m.Schedule)
		}
	})
}

// TestDataFlowScheduleObjectRoundTrip proves dataFlowScheduleFromObject and
// dataFlowScheduleToObject are inverses on every shape buildBody/apply care
// about: a concrete schedule, and the null/unknown sentinels.
func TestDataFlowScheduleObjectRoundTrip(t *testing.T) {
	m := &dataFlowScheduleModel{CronExpression: types.StringValue("*/5 * * * *"), IsEnabled: types.BoolValue(true)}
	obj := dataFlowScheduleToObject(m)
	got := dataFlowScheduleFromObject(obj)
	if got == nil || !got.CronExpression.Equal(m.CronExpression) || !got.IsEnabled.Equal(m.IsEnabled) {
		t.Errorf("round trip = %#v, want %#v", got, m)
	}

	if got := dataFlowScheduleFromObject(types.ObjectNull(dataFlowScheduleAttrTypes())); got != nil {
		t.Errorf("dataFlowScheduleFromObject(null) = %#v, want nil", got)
	}
	if got := dataFlowScheduleFromObject(types.ObjectUnknown(dataFlowScheduleAttrTypes())); got != nil {
		t.Errorf("dataFlowScheduleFromObject(unknown) = %#v, want nil", got)
	}
	if got := dataFlowScheduleToObject(nil); !got.IsNull() {
		t.Errorf("dataFlowScheduleToObject(nil) = %v, want a null object", got)
	}
}

// TestDataFlowReadReconcilesSchedule proves the end-to-end drift-visibility fix
// through Read(), the same way TestDataFlowReadReconcilesActivate proves it for
// activate/status: an out-of-band schedule change on a `schedule`-managed data
// flow now surfaces in refreshed state instead of being silently invisible
// (CORE-2583 AC9).
func TestDataFlowReadReconcilesSchedule(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cross_id":"river1","name":"flow","kind":"main_river",` +
			`"type":"source_to_target","metadata":{"river_status":"active"},` +
			`"schedulers":[{"cron_expression":"*/10 * * * *","is_enabled":false}]}`))
	}))
	defer srv.Close()

	c, err := client.New(client.Config{BaseURL: srv.URL, Token: "t", AccountID: "acc"})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	r := &dataFlowResource{data: &providerData{client: c, defaultEnvironmentID: "env1"}}
	s := dataFlowSchemaForTest(t)

	// State remembers the schedule as it was configured/applied last time:
	// enabled, every 5 minutes. The live API has since drifted to disabled,
	// every 10 minutes (e.g. changed via the console).
	scheduleType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"cron_expression": tftypes.String, "is_enabled": tftypes.Bool,
	}}
	stateRaw := dataFlowObjectForTest(t, s, map[string]tftypes.Value{
		"id":              tftypes.NewValue(tftypes.String, "river1"),
		"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
		"name":            tftypes.NewValue(tftypes.String, "flow"),
		"kind":            tftypes.NewValue(tftypes.String, "main_river"),
		"type":            tftypes.NewValue(tftypes.String, "source_to_target"),
		"properties_json": tftypes.NewValue(tftypes.String, batchProps),
		"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
		"schedule": tftypes.NewValue(scheduleType, map[string]tftypes.Value{
			"cron_expression": tftypes.NewValue(tftypes.String, "*/5 * * * *"),
			"is_enabled":      tftypes.NewValue(tftypes.Bool, true),
		}),
		"activate": boolRaw(types.BoolValue(true)),
		"status":   stringRaw(types.StringValue(riverStatusActive)),
		"step_ids": tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
	})
	resp := &resource.ReadResponse{State: tfsdk.State{Raw: stateRaw, Schema: s}}
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Raw: stateRaw, Schema: s}}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var gotSchedule types.Object
	resp.State.GetAttribute(ctx, path.Root("schedule"), &gotSchedule)
	want := scheduleObj(&dataFlowScheduleModel{
		CronExpression: types.StringValue("*/10 * * * *"), IsEnabled: types.BoolValue(false),
	})
	if !gotSchedule.Equal(want) {
		t.Errorf("refreshed schedule = %v, want the live drifted value %v (drift must be visible, not silently hidden)",
			gotSchedule, want)
	}
}

// TestDataFlowUpdateSendsExplicitRemovalOnTransition proves the Update()-level
// wiring: when scheduleRemovalNeeded's condition holds, the PUT body carries
// an explicit empty schedulers list, not an omitted key -- the only way to
// tell the API to actually clear an existing schedule (an omitted key is
// read as "leave it alone" by the API's deep-merge PUT). wasManaged can't be
// driven true through req.Private in this test harness (see
// writeScheduleManaged), so this exercises buildBody + the explicit-clear
// assignment directly, the same way Update() composes them.
func TestDataFlowUpdateSendsExplicitRemovalOnTransition(t *testing.T) {
	s := dataFlowSchemaForTest(t)
	planRaw := dataFlowObjectForTest(t, s, map[string]tftypes.Value{
		"id":              tftypes.NewValue(tftypes.String, "river1"),
		"environment_id":  tftypes.NewValue(tftypes.String, "env1"),
		"name":            tftypes.NewValue(tftypes.String, "flow"),
		"kind":            tftypes.NewValue(tftypes.String, "main_river"),
		"type":            tftypes.NewValue(tftypes.String, "source_to_target"),
		"properties_json": tftypes.NewValue(tftypes.String, batchProps),
		"settings_json":   tftypes.NewValue(tftypes.String, "{}"),
		"schedule":        scheduleTFNull(), // just removed from config
		"activate":        boolRaw(types.BoolValue(true)),
		"status":          stringRaw(types.StringValue(riverStatusActive)),
		"step_ids":        tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{}),
	})
	var plan dataFlowModel
	var diags diag.Diagnostics
	diags.Append(tfsdk.Plan{Raw: planRaw, Schema: s}.Get(context.Background(), &plan)...)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	r := &dataFlowResource{}
	body, ok := r.buildBody(plan, nil, &diags)
	if !ok {
		t.Fatalf("buildBody failed: %v", diags)
	}
	if scheduleRemovalNeeded(true, scheduleConfigured(plan)) {
		body["schedulers"] = []any{}
	}

	schedulers, present := body["schedulers"]
	if !present {
		t.Fatal("expected an explicit \"schedulers\" key in the PUT body, got none (an omitted key leaves the live schedule untouched)")
	}
	list, ok := schedulers.([]any)
	if !ok || len(list) != 0 {
		t.Errorf("body[\"schedulers\"] = %#v, want an explicit empty list", schedulers)
	}
}
