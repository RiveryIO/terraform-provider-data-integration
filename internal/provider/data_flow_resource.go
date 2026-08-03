package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*dataFlowResource)(nil)
	_ resource.ResourceWithConfigure   = (*dataFlowResource)(nil)
	_ resource.ResourceWithImportState = (*dataFlowResource)(nil)
)

// NewDataFlowResource is the factory registered with the provider.
func NewDataFlowResource() resource.Resource { return &dataFlowResource{} }

type dataFlowResource struct {
	data *providerData
}

type dataFlowModel struct {
	ID             types.String         `tfsdk:"id"`
	EnvironmentID  types.String         `tfsdk:"environment_id"`
	Name           types.String         `tfsdk:"name"`
	Kind           types.String         `tfsdk:"kind"`
	Type           types.String         `tfsdk:"type"`
	Description    types.String         `tfsdk:"description"`
	PropertiesJSON jsontypes.Normalized `tfsdk:"properties_json"`
	SettingsJSON   jsontypes.Normalized `tfsdk:"settings_json"`
	SchedulersJSON jsontypes.Normalized `tfsdk:"schedulers_json"`
	GroupID        types.String         `tfsdk:"group_id"`
	Activate       types.Bool           `tfsdk:"activate"`
	Status         types.String         `tfsdk:"status"`
	StepIDs        types.List           `tfsdk:"step_ids"`
}

func (r *dataFlowResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_data_flow"
}

func (r *dataFlowResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Data Integration data flow (the API calls this a \"river\"). The flow " +
			"definition is supplied as JSON via properties_json; this keeps the resource forward-" +
			"compatible with the full data flow schema without re-modelling every field.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Data flow ID (cross_id), assigned by the API.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment this data flow belongs to. Falls back to the provider-" +
					"level environment_id. Changing it forces a new data flow.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Data flow name.",
			},
			"kind": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("main_river"),
				Description: "Data flow kind. Defaults to \"main_river\".",
			},
			"type": schema.StringAttribute{
				Required:    true,
				Description: "Data flow type. One of: \"source_to_target\", \"logic\", \"actions\", \"connector_executor\".",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Description. Stored under the API's metadata.description (a top-level description is rejected).",
			},
			"properties_json": schema.StringAttribute{
				Required:   true,
				CustomType: jsontypes.NormalizedType{},
				Description: "The data flow properties object as JSON — must include a properties_type " +
					"discriminator and (for logic flows) a non-empty logic_steps array. This value " +
					"is config-authoritative: the API enriches it on write (logic_steps gain " +
					"step_id/is_enabled/…), so the provider keeps your configured value and does not " +
					"refresh it from the API. Drift inside this blob is therefore not detected. " +
					"NATIVE CONNECTORS (run_type=\"multi_tables\", is_native — e.g. github): their " +
					"required Source Settings live under source.additional_settings.interface_parameters." +
					"source[] and are NOT validated by this provider. Discover the mandatory ones with " +
					"GET .../data_source_properties/global_properties?datasource_id=<slug> — every entry " +
					"in cross_reports_predefined[] with \"required\": true must be supplied (e.g. github " +
					"requires organization AND repositories). See the buildBody note for the value-format " +
					"rules and how to verify. " +
					"EXTRACT_METHOD (per-table, under each schemas[].tables[].details): ExtractMethodEnum " +
					"is \"all\" | \"incremental\" | \"log\" | \"change_tracking\" | \"system_versioning\". " +
					"For RDBMS sources this field is enum-validated server-side and the correct spelling " +
					"is \"incremental\" — NOT \"increment\". (\"increment\" is silently tolerated on " +
					"SaaS/predefined-report source tables, whose extract_method is an untyped free string, " +
					"but it is WRONG for database sources and should not be copied from SaaS examples.) " +
					"Incremental extraction additionally requires details.incremental_field (the source " +
					"column driving the increment) plus exactly ONE of the three mutually-exclusive mode " +
					"objects details.date_range / details.running_number / details.epoch — set only one, " +
					"and set details.is_custom_incremental = false. date_range shape: " +
					"{time_period: RiverTimePeriodEnum (e.g. \"custom\", \"year_to_date\", \"last_7_days\"), " +
					"start_date, end_date (RFC3339 or null), days_back, include_end_value, " +
					"split_time_intervals: {time_interval: IntervalTimeExternalEnum (e.g. \"dont_split\", " +
					"\"days\"), interval_size}, update_increment_on_failures, utc_offset, round_up}. To " +
					"backfill from a fixed date then track forward: time_period=\"custom\" + " +
					"start_date=\"<date>\", leaving end_date null. " +
					"boomi_data_integration_source_metadata can generate this whole incremental mapping for " +
					"you (its extract_method/incremental_field/date_range inputs) — decode its schemas_json " +
					"output straight into this field.",
			},
			"settings_json": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				CustomType: jsontypes.NormalizedType{},
				Default:    stringdefault.StaticString("{}"),
				Description: "The data flow settings object as JSON. Defaults to an empty object. " +
					"Config-authoritative like properties_json (the API adds a notification block " +
					"on write); the provider does not refresh it from the API.",
			},
			"schedulers_json": schema.StringAttribute{
				Optional:   true,
				CustomType: jsontypes.NormalizedType{},
				Description: "The data flow schedule as a JSON array, sent top-level as \"schedulers\". " +
					"Each item is {\"cron_expression\": \"<5-field UNIX cron>\", \"is_enabled\": true}. " +
					"REQUIRED for CDC (log-based) data flows: the API rejects creating or enabling a CDC " +
					"data flow without an enabled scheduler (\"Please schedule a CDC data flow before enabling " +
					"or creating\"), and the cron must run between once per day and 12 times per hour " +
					"(i.e. a 5-minute-to-24-hour interval). Exactly one scheduler is allowed. Optional for " +
					"non-CDC flows. Config-authoritative like properties_json/settings_json: the provider " +
					"keeps your configured value and does not refresh it from the API.",
			},
			"group_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Group (cross_id) the data flow belongs to. " +
					"Set to a valid group ID to place the data flow in a specific group, " +
					"or null to let the platform assign one automatically.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"activate": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Description: "Desired activation state of the data flow. " +
					"true: the provider enables the flow, running [disable, if it is currently " +
					"active] → update → [enable_cdc, for a CDC flow] → activate. The update step " +
					"initialises " +
					"the fire-service task entry the activate API requires — data flows created " +
					"through the API lack that entry until their first PUT — and enable_cdc sets " +
					"ENABLE_LOG on a log-based (CDC) flow, which nothing else does for a flow that " +
					"is CDC from its very first apply (the endpoint answers 204 when CDC is already " +
					"enabled, so it is safe to repeat). " +
					"false: the provider disables the flow if it is currently active. " +
					"OMITTED: activation is not managed — there is deliberately no default. The " +
					"provider adopts whatever the server reports (a newly created data flow is " +
					"disabled) and never activates or disables the flow on later applies. " +
					"Refresh reconciles this attribute against the API's metadata.river_status, so " +
					"an activate/disable performed outside this resource — the console, the API, or " +
					"the data_flow_run resource — shows up as drift on the next plan and, when " +
					"activate is set explicitly, is corrected on the next apply. When the API omits " +
					"river_status (observed on a small fraction of data flows) the previously known " +
					"value is kept. See the read-only status attribute for the raw server value.",
			},
			"status": schema.StringAttribute{
				Computed: true,
				Description: "Server-side activation status, from the API's metadata.river_status: " +
					"\"active\" or \"disabled\". Read from the plain data flow GET — this provider is " +
					"a desired-state tool and never consults the operations/activities layer. Null " +
					"when the API response omits the field (observed on a small fraction of data " +
					"flows). Read-only: change activation through activate.",
				PlanModifiers: []planmodifier.String{activationStatusModifier{}},
			},
			"step_ids": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
				Description: "Stable step IDs for logic data flow steps, auto-generated on first create " +
					"and preserved across updates. The provider injects these into the logic_steps " +
					"array before each API write so step_id does not need to appear in properties_json. " +
					"Positional: index 0 corresponds to the first step, index 1 to the second, etc. " +
					"Non-logic data flows always have an empty list.",
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *dataFlowResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *dataFlowResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dataFlowModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	envID := resolveEnvironmentID(plan.EnvironmentID.ValueString(), r.data)
	if envID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("environment_id"), "Missing environment_id",
			"Set environment_id on the resource or environment_id on the provider.")
		return
	}

	body, ok := r.buildBody(plan, nil, &resp.Diagnostics)
	if !ok {
		return
	}

	created, err := r.data.client.CreateDataFlow(ctx, envID, body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating data flow", err)
		return
	}
	resp.Diagnostics.Append(r.apply(created, envID, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Post-create activation. Its diagnostics are surfaced, but state is still
	// written below: from the POST on, the data flow exists server-side, and
	// dropping it from state makes the next apply create a duplicate (observed
	// as duplicate data flows when CDC activation failed midway).
	activated, activateDiags := r.activateOnCreate(ctx, envID, plan, body)
	resp.Diagnostics.Append(activateDiags...)

	plan.Status = resolveStatus(plan.Status, created, activated, false)
	if plan.Activate.IsUnknown() || plan.Activate.IsNull() {
		// activate was left out of the configuration (it has no default):
		// record what actually happened instead of guessing.
		plan.Activate = types.BoolValue(activated)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// activateOnCreate runs the post-POST sequence that turns a freshly created data
// flow into an active one, in the only order the API accepts:
//
//	PUT        initialises the fire-service task entry the activate operation
//	           requires; without it activation fails with RVR-ACTIVATE-500 on
//	           every API-created data flow.
//	enable_cdc only for a CDC (log-based) flow. Such a flow never goes through
//	           Update's switch-to-CDC transition, so without this nothing ever
//	           sets ENABLE_LOG: activation reports success while the flow is
//	           silently not CDC-enabled. The endpoint answers 204 when CDC is
//	           already enabled, so calling it is safe even if it is redundant.
//	activate   with the longer CDC timeout for CDC flows, which take noticeably
//	           longer to reach ACTIVE after enable_cdc.
//
// It reports whether the data flow ended up activated. A failure at any step is
// returned as a diagnostic rather than swallowed — the caller must not treat a
// partially activated flow as a success.
func (r *dataFlowResource) activateOnCreate(
	ctx context.Context, envID string, plan dataFlowModel, body map[string]any,
) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !plan.Activate.ValueBool() {
		return false, diags
	}
	id := plan.ID.ValueString()

	if _, err := r.data.client.UpdateDataFlow(ctx, envID, id, body); err != nil {
		addAPIError(&diags, "Error initialising data flow before activation", err)
		return false, diags
	}

	isCDC := isCDCFlow(plan.PropertiesJSON.ValueString())
	if isCDC {
		diags.Append(r.enableCDC(ctx, envID, id)...)
		if diags.HasError() {
			return false, diags
		}
	}

	activateTimeout := dataFlowOpTimeout
	if isCDC {
		activateTimeout = cdcEnableOpTimeout
	}
	activateDiags := r.activateFlow(ctx, envID, id, activateTimeout)
	diags.Append(activateDiags...)
	return !activateDiags.HasError(), diags
}

func (r *dataFlowResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dataFlowModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	df, err := r.data.client.GetDataFlow(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading data flow", err)
		return
	}
	resp.Diagnostics.Append(r.apply(df, state.EnvironmentID.ValueString(), &state)...)

	// Refresh the activation state from the server. river_status rides along on
	// the plain data flow GET, so observing activation costs no extra call and —
	// importantly for a desired-state provider — needs no operations/activities
	// lookup.
	//
	// activate is reconciled here and only here: Create/Update must return
	// exactly the value Terraform planned, so apply() (shared by all three) must
	// not touch it. An absent river_status leaves the previously known activate
	// value alone rather than guessing "disabled".
	state.Status = statusFromAPI(df)
	if active, ok := activeFromRiverStatus(df); ok {
		state.Activate = types.BoolValue(active)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// River activation states, as reported by the API's metadata.river_status
// (RiverStatusEnum).
const (
	riverStatusActive   = "active"
	riverStatusDisabled = "disabled"
)

// riverStatus extracts metadata.river_status from a data flow API response body.
// ok is false when metadata, or river_status inside it, is absent — measured on
// a small fraction of real GET bodies — so callers can tell "not reported" apart
// from "disabled" instead of writing a bogus value.
func riverStatus(api map[string]any) (string, bool) {
	meta, isMap := api["metadata"].(map[string]any)
	if !isMap {
		return "", false
	}
	status := asString(meta["river_status"])
	if status == "" {
		return "", false
	}
	return status, true
}

// statusFromAPI maps metadata.river_status onto the status attribute, leaving it
// null when the API does not report one.
func statusFromAPI(api map[string]any) types.String {
	if status, ok := riverStatus(api); ok {
		return types.StringValue(status)
	}
	return types.StringNull()
}

// activeFromRiverStatus reports whether a data flow is active according to
// metadata.river_status. ok is false when the API does not report a status.
func activeFromRiverStatus(api map[string]any) (active, ok bool) {
	status, ok := riverStatus(api)
	if !ok {
		return false, false
	}
	return status == riverStatusActive, true
}

// resolveStatus decides the status value an apply writes to state.
//
//   - When Terraform planned a concrete value — including a deliberate null —
//     that value is returned unchanged. Anything else would break Terraform's
//     plan==apply consistency contract; the next refresh reconciles it. Keeping
//     this first means a mis-scoped plan modifier can only ever leave status
//     stale for one refresh, never fail an apply.
//   - Otherwise (status planned as unknown) report what this apply achieved.
//     Deliberately not read back from the write response: the PUT runs while the
//     data flow is disabled, so its metadata.river_status says "disabled" even on
//     an apply that activates the flow. What the provider does know is which
//     activation calls it made and that they polled through to success.
//   - Failing that (create without activation, or an activation step that
//     errored) fall back to the API response, which may legitimately carry no
//     river_status at all.
func resolveStatus(planned types.String, api map[string]any, activated, deactivated bool) types.String {
	if !planned.IsUnknown() {
		return planned
	}
	switch {
	case activated:
		return types.StringValue(riverStatusActive)
	case deactivated:
		return types.StringValue(riverStatusDisabled)
	default:
		return statusFromAPI(api)
	}
}

// activationStatusModifier keeps the Computed status attribute plan-consistent.
//
// Terraform proposes the PRIOR value for a Computed attribute the configuration
// cannot touch, so returning a different value after apply is a "Provider
// produced inconsistent result after apply" error. status genuinely changes
// during an apply that activates or disables the flow, so it is marked unknown
// for exactly those plans. Marking it unknown unconditionally would be worse: it
// would turn every refresh-only plan into a permanent no-op update
// (status: "active" -> (known after apply)).
type activationStatusModifier struct{}

func (activationStatusModifier) Description(_ context.Context) string {
	return "Planned as \"known after apply\" when the apply will activate or disable the data flow."
}

func (m activationStatusModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (activationStatusModifier) PlanModifyString(
	ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse,
) {
	// Nothing to adjust on create (no prior state — the framework already plans
	// an unknown status) or destroy (no plan).
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var planActivate, stateActivate types.Bool
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("activate"), &planActivate)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("activate"), &stateActivate)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if statusMayChange(planActivate, stateActivate, req.StateValue) {
		resp.PlanValue = types.StringUnknown()
		return
	}
	// Otherwise plan exactly what state holds — including a null status, for the
	// data flows whose API response carries no river_status at all. Terraform
	// plans a null Computed attribute as unknown, which would otherwise surface
	// as a no-op "update in place" on every single plan.
	resp.PlanValue = req.StateValue
}

// statusMayChange reports whether an apply can move the data flow's
// river_status.
//
//   - A planned activation different from the recorded one will activate or
//     disable the flow. An unknown/null planned activate errs on "may change".
//   - A recorded status that contradicts the recorded activation (left behind by
//     an apply whose activation step errored) can still be repaired by this
//     apply.
//   - A status the API never reported stays unreported: nothing to plan.
func statusMayChange(planActivate, stateActivate types.Bool, stateStatus types.String) bool {
	if planActivate.IsUnknown() || planActivate.IsNull() {
		return true
	}
	if planActivate.ValueBool() != stateActivate.ValueBool() {
		return true
	}
	if stateStatus.IsNull() || stateStatus.IsUnknown() {
		return false
	}
	return (stateStatus.ValueString() == riverStatusActive) != stateActivate.ValueBool()
}

func (r *dataFlowResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state dataFlowModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envID := plan.EnvironmentID.ValueString()

	// activate has no default, so an unknown/null planned value means "not
	// managed" — keep the flow in whatever state it is currently in. Otherwise
	// the plan value is the desired state and drives disable/activate below.
	wantActive := plan.Activate.ValueBool()
	if plan.Activate.IsUnknown() || plan.Activate.IsNull() {
		wantActive = state.Activate.ValueBool()
	}
	wasActive := state.Activate.ValueBool()

	// Inject stored step_ids so existing steps keep their IDs across updates.
	// New steps (beyond the stored count) get no step_id — the API generates them.
	body, ok := r.buildBody(plan, listToStepIDs(state.StepIDs), &resp.Diagnostics)
	if !ok {
		return
	}

	// Detect transition to CDC (log-based) extract method. When switching to log,
	// the API validator requires a scheduler in the PUT body. We fetch the existing
	// schedulers from the GET response and inject them so the validator passes.
	switchingToCDC := isCDCFlow(plan.PropertiesJSON.ValueString()) &&
		!isCDCFlow(state.PropertiesJSON.ValueString())

	if switchingToCDC {
		// When the config supplies schedulers_json, buildBody has already set it and
		// that takes precedence. Otherwise fall back to the schedulers currently on
		// the data flow so the CDC validator still passes.
		if _, hasSchedulers := body["schedulers"]; !hasSchedulers {
			current, err := r.data.client.GetDataFlow(ctx, envID, plan.ID.ValueString())
			if err != nil {
				addAPIError(&resp.Diagnostics, "Error fetching data flow schedulers for CDC transition", err)
				return
			}
			if schedulers, ok := current["schedulers"]; ok {
				body["schedulers"] = schedulers
			}
		}
	}

	deactivated := false
	if wantActive || wasActive {
		// Disable before editing — the API requires the data flow to be inactive
		// during a PUT when it is currently active. Gating on wasActive (the
		// refreshed server state — see Read) as well as wantActive means this also
		// fires on an activate true -> false transition, and since we do not
		// re-activate below when wantActive is false, that single disable call is
		// what makes activate = false actually disable the flow instead of being a
		// silently-ignored write-intent.
		if opID, err2 := r.data.client.DisableDataFlow(ctx, envID, plan.ID.ValueString()); err2 != nil {
			addAPIError(&resp.Diagnostics, "Error disabling data flow before update", err2)
			return
		} else if opID != "" {
			if !r.waitForOp(ctx, envID, opID, &resp.Diagnostics) {
				return
			}
		}
		deactivated = wasActive && !wantActive
	}

	updated, err := r.data.client.UpdateDataFlow(ctx, envID, plan.ID.ValueString(), body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating data flow", err)
		return
	}
	resp.Diagnostics.Append(r.apply(updated, envID, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// CDC must be enabled before a CDC data flow can run: the operation sets
	// ENABLE_LOG=true. Run it when this update switches the flow into CDC, and
	// also whenever we are about to activate a CDC flow — a flow created as CDC
	// with activate omitted/false has never been through either path, so this is
	// what makes "flip activate to true later" work. The endpoint answers 204
	// (no operation) when CDC is already enabled, so the redundant call in the
	// already-enabled case is a cheap no-op rather than a re-initialisation.
	isCDC := isCDCFlow(plan.PropertiesJSON.ValueString())
	if switchingToCDC || (isCDC && wantActive) {
		resp.Diagnostics.Append(r.enableCDC(ctx, envID, plan.ID.ValueString())...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	activated := false
	if wantActive {
		// CDC data flows take longer to activate after enabling CDC — use the extended timeout.
		activateTimeout := dataFlowOpTimeout
		if isCDC {
			activateTimeout = cdcEnableOpTimeout
		}
		activateDiags := r.activateFlow(ctx, envID, plan.ID.ValueString(), activateTimeout)
		resp.Diagnostics.Append(activateDiags...)
		activated = !activateDiags.HasError()
	}

	plan.Activate = types.BoolValue(wantActive)
	plan.Status = resolveStatus(plan.Status, updated, activated, deactivated)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dataFlowResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dataFlowModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envID := state.EnvironmentID.ValueString()
	id := state.ID.ValueString()
	// Disable before delete — the API rejects DELETE on an active data flow.
	// DisableDataFlow is async; poll until done before issuing DELETE.
	if opID, err := r.data.client.DisableDataFlow(ctx, envID, id); err != nil && !errors.Is(err, client.ErrNotFound) {
		addAPIError(&resp.Diagnostics, "Error disabling data flow before delete", err)
		return
	} else if opID != "" {
		if !r.waitForOp(ctx, envID, opID, &resp.Diagnostics) {
			return
		}
	}
	if err := r.data.client.DeleteDataFlow(ctx, envID, id); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		addAPIError(&resp.Diagnostics, "Error deleting data flow", err)
	}
}

func (r *dataFlowResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	envID, id, err := splitImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	if envID == "" {
		envID = resolveEnvironmentID("", r.data)
	}
	if envID == "" {
		resp.Diagnostics.AddError("Missing environment_id for import",
			"Use \"<environment_id>/<data_flow_id>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}

// buildBody assembles the API write body from the plan, applying the schema
// rules learned validating against the live API: metadata/settings objects are
// required, description lives under metadata.description, properties carries the
// properties_type discriminator.
const dataFlowOpTimeout = 5 * time.Minute
const cdcEnableOpTimeout = 10 * time.Minute

// activateFlow calls activate and polls the async operation.
// Pass timeout=cdcEnableOpTimeout when activating immediately after enabling CDC,
// since CDC data flows take longer to reach ACTIVE state.
func (r *dataFlowResource) activateFlow(ctx context.Context, envID, id string, timeout time.Duration) diag.Diagnostics {
	var diags diag.Diagnostics
	opID, err := r.data.client.ActivateDataFlow(ctx, envID, id)
	if err != nil {
		addAPIError(&diags, "Error activating data flow", err)
		return diags
	}
	if opID != "" {
		r.waitForOpWithTimeout(ctx, envID, opID, timeout, &diags)
	}
	return diags
}

// enableCDC calls the CDC-enable endpoint and polls the async operation.
// Required after switching a data flow to extract_method=log before it can run.
// Uses a longer timeout since the operation sets up CDC binlog readers and
// can take several minutes.
func (r *dataFlowResource) enableCDC(ctx context.Context, envID, id string) diag.Diagnostics {
	var diags diag.Diagnostics
	opID, err := r.data.client.EnableCDCDataFlow(ctx, envID, id)
	if err != nil {
		addAPIError(&diags, "Error enabling CDC for data flow", err)
		return diags
	}
	if opID != "" {
		r.waitForOpWithTimeout(ctx, envID, opID, cdcEnableOpTimeout, &diags)
	}
	return diags
}

// isCDCFlow returns true when the properties_json selects log-based extraction.
func isCDCFlow(propertiesJSON string) bool {
	var props struct {
		Source struct {
			AdditionalSettings struct {
				ExtractMethod string `json:"extract_method"`
			} `json:"additional_settings"`
		} `json:"source"`
	}
	if err := json.Unmarshal([]byte(propertiesJSON), &props); err != nil {
		return false
	}
	return props.Source.AdditionalSettings.ExtractMethod == "log"
}

// waitForOp polls an async operation until done ("D") or error/timeout.
func (r *dataFlowResource) waitForOp(ctx context.Context, envID, opID string, diags *diag.Diagnostics) bool {
	return r.waitForOpWithTimeout(ctx, envID, opID, dataFlowOpTimeout, diags)
}

func (r *dataFlowResource) waitForOpWithTimeout(ctx context.Context, envID, opID string, timeout time.Duration, diags *diag.Diagnostics) bool {
	deadline := time.Now().Add(timeout)
	for {
		status, errMsg, err := r.data.client.GetOperation(ctx, envID, opID)
		if err != nil {
			addAPIError(diags, "Error polling operation", err)
			return false
		}
		switch status {
		case "D":
			return true
		case "E":
			diags.AddError("Data flow operation failed",
				fmt.Sprintf("operation %s reported an error: %s", opID, errMsg))
			return false
		}
		if time.Now().After(deadline) {
			diags.AddError("Data flow operation timed out",
				fmt.Sprintf("operation %s did not finish within %s (last status %q)", opID, timeout, status))
			return false
		}
		select {
		case <-ctx.Done():
			diags.AddError("Data flow operation cancelled", ctx.Err().Error())
			return false
		case <-time.After(2 * time.Second):
		}
	}
}

func (r *dataFlowResource) buildBody(plan dataFlowModel, stepIDs []string, diags *diag.Diagnostics) (map[string]any, bool) {
	props, ok := decodeJSONObject(plan.PropertiesJSON, path.Root("properties_json"), diags)
	if !ok {
		return nil, false
	}
	injectStepIDsIntoProps(props, stepIDs)
	settings := map[string]any{}
	if !plan.SettingsJSON.IsNull() && !plan.SettingsJSON.IsUnknown() {
		s, ok := decodeJSONObject(plan.SettingsJSON, path.Root("settings_json"), diags)
		if !ok {
			return nil, false
		}
		settings = s
	}

	metadata := map[string]any{}
	if !plan.Description.IsNull() {
		metadata["description"] = plan.Description.ValueString()
	}

	body := map[string]any{
		"name":       plan.Name.ValueString(),
		"kind":       plan.Kind.ValueString(),
		"type":       plan.Type.ValueString(),
		"metadata":   metadata,
		"settings":   settings,
		"properties": props,
	}
	if !plan.GroupID.IsNull() && !plan.GroupID.IsUnknown() && plan.GroupID.ValueString() != "" {
		body["group_id"] = plan.GroupID.ValueString()
	}
	// schedulers is a top-level list on the write body. It is mandatory for CDC
	// (log-based) flows — the API validates that an enabled scheduler is present
	// before it will create or enable a CDC data flow.
	//
	// NATIVE-CONNECTOR SOURCE SETTINGS (verify checklist) — properties_json is
	// opaque to this provider, so nothing here validates that a native connector's
	// required "Source Settings" are present. They are the fields the console shows
	// under "Source Settings — Connector settings applied to every report" (marked
	// with a red *). A data flow created without them saves fine but is unusable (the UI
	// shows the required dropdowns empty; a run has no scope). To author one correctly:
	//
	//  1. Identify a native connector: its data_source_types entry has is_native=true
	//     and feature_flags.run_types=["multi_tables"]; in properties_json the source
	//     is name="native_connector" with additional_settings.nc_id/nc_version set.
	//  2. Discover the required settings:
	//       GET .../data_source_properties/global_properties?datasource_id=<slug>
	//     Each cross_reports_predefined[] descriptor with "required": true is
	//     mandatory. (GitHub → organization AND repositories.)
	//  3. Supply every required descriptor under:
	//       source.additional_settings.interface_parameters.source = [{name,type,value}]
	//     - name MUST equal the descriptor's name exactly (a mismatch = the console
	//       dropdown renders empty even though the value was saved).
	//     - value format follows the descriptor type: list_api_single_id →
	//       one API-resolved id, list_api_multiple_id → a list of API-resolved ids
	//       (NOT raw display strings), input_text → the literal string.
	//  4. Verify: after apply, GET the data flow and confirm interface_parameters.source
	//     round-trips, and that the console Source tab pre-selects the values (not
	//     "Select..."). A test-connection on the source connection is recommended too.
	if !plan.SchedulersJSON.IsNull() && !plan.SchedulersJSON.IsUnknown() {
		schedulers, ok := decodeJSONArray(plan.SchedulersJSON, path.Root("schedulers_json"), diags)
		if !ok {
			return nil, false
		}
		body["schedulers"] = schedulers
	}
	return body, true
}

// apply maps an API response onto the model, normalizing read shape so an
// imported or refreshed data flow plans clean.
func (r *dataFlowResource) apply(api map[string]any, envID string, m *dataFlowModel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(asString(api["id"]))
	m.EnvironmentID = types.StringValue(envID)
	m.Name = types.StringValue(asString(api["name"]))
	if k := asString(api["kind"]); k != "" {
		m.Kind = types.StringValue(k)
	}
	if t := asString(api["type"]); t != "" {
		m.Type = types.StringValue(t)
	}

	// description <- metadata.description.
	//
	// activate and status are NOT set here on purpose. Both depend on the caller:
	// Read reconciles them from metadata.river_status, while Create/Update must
	// return the value Terraform planned (see resolveStatus) — a write response
	// reports the status the flow had mid-apply, which is not what the apply
	// achieved.
	if meta, ok := api["metadata"].(map[string]any); ok {
		if d := asString(meta["description"]); d != "" {
			m.Description = types.StringValue(d)
		} else if m.Description.IsUnknown() {
			m.Description = types.StringNull()
		}
	}

	// group_id is a plain scalar the API echoes back; adopt the server value so a
	// Computed (unset) group_id lands the API-assigned one without a perpetual diff.
	if g := asString(api["group_id"]); g != "" {
		m.GroupID = types.StringValue(g)
	} else if m.GroupID.IsUnknown() {
		m.GroupID = types.StringNull()
	}

	// Always read step_ids from the API response — the API is the source of truth.
	// On create the API generates them; on update it echoes back what we injected.
	if props, ok := api["properties"].(map[string]any); ok {
		m.StepIDs = stepIDsToList(extractStepIDs(props))
	}
	if m.StepIDs.IsNull() || m.StepIDs.IsUnknown() {
		m.StepIDs = stepIDsToList(nil)
	}

	// properties_json / settings_json are config-authoritative JSON passthrough.
	// The API enriches them on write (logic_steps gain step_id/is_enabled/…,
	// settings gains a notification block), so echoing the server value back
	// would (a) break Terraform's plan==apply consistency contract and (b)
	// produce a perpetual diff on refresh. We therefore preserve the configured
	// value and only populate from the server when it is absent — i.e. on
	// import, where there is no prior config to honor.
	if m.PropertiesJSON.IsNull() || m.PropertiesJSON.IsUnknown() {
		if props, ok := api["properties"].(map[string]any); ok {
			// Strip step_ids so imported properties_json matches what a user writes.
			stripStepIDsFromProps(props)
			if raw, err := json.Marshal(props); err == nil {
				m.PropertiesJSON = jsontypes.NewNormalizedValue(string(raw))
			} else {
				diags.AddError("Error encoding properties", err.Error())
			}
		}
	}
	if m.SettingsJSON.IsNull() || m.SettingsJSON.IsUnknown() {
		if settings, ok := api["settings"].(map[string]any); ok {
			if raw, err := json.Marshal(settings); err == nil {
				m.SettingsJSON = jsontypes.NewNormalizedValue(string(raw))
			}
		} else {
			m.SettingsJSON = jsontypes.NewNormalizedValue("{}")
		}
	}
	// schedulers_json is Optional (not Computed), so an unset config is null. Only
	// seed it from the API when it is null AND the data flow actually carries a
	// scheduler — i.e. on import. Never populate from an empty API list, which
	// would turn a legitimately-null config into "[]" and break plan==apply
	// consistency on create.
	if m.SchedulersJSON.IsNull() || m.SchedulersJSON.IsUnknown() {
		if schedulers, ok := api["schedulers"].([]any); ok && len(schedulers) > 0 {
			if raw, err := json.Marshal(schedulers); err == nil {
				m.SchedulersJSON = jsontypes.NewNormalizedValue(string(raw))
			}
		}
	}
	return diags
}

// decodeJSONObject parses a Normalized JSON string into a map, appending a
// diagnostic at attrPath when it is not a JSON object.
func decodeJSONObject(v jsontypes.Normalized, attrPath path.Path, diags *diag.Diagnostics) (map[string]any, bool) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(v.ValueString()), &obj); err != nil {
		diags.AddAttributeError(attrPath, "Invalid JSON object",
			fmt.Sprintf("%s must be a JSON object: %s", attrPath, err))
		return nil, false
	}
	return obj, true
}

// decodeJSONArray parses a Normalized JSON string into a slice, appending a
// diagnostic at attrPath when it is not a JSON array.
func decodeJSONArray(v jsontypes.Normalized, attrPath path.Path, diags *diag.Diagnostics) ([]any, bool) {
	var arr []any
	if err := json.Unmarshal([]byte(v.ValueString()), &arr); err != nil {
		diags.AddAttributeError(attrPath, "Invalid JSON array",
			fmt.Sprintf("%s must be a JSON array: %s", attrPath, err))
		return nil, false
	}
	return arr, true
}

// ── step_id helpers ───────────────────────────────────────────────────────────

// injectStepIDsIntoProps writes the step_ids slice into the logic_steps array
// inside props (the parsed properties object), overwriting any step_id already
// present. No-op when props has no logic_steps or stepIDs is empty.
func injectStepIDsIntoProps(props map[string]any, stepIDs []string) {
	if len(stepIDs) == 0 {
		return
	}
	steps, ok := props["logic_steps"].([]any)
	if !ok {
		return
	}
	for i, raw := range steps {
		if i >= len(stepIDs) {
			break
		}
		step, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		step["step_id"] = stepIDs[i]
		steps[i] = step
	}
}

// extractStepIDs reads the step_id from each step in a parsed properties object.
func extractStepIDs(props map[string]any) []string {
	steps, ok := props["logic_steps"].([]any)
	if !ok || len(steps) == 0 {
		return nil
	}
	ids := make([]string, len(steps))
	for i, raw := range steps {
		if step, ok := raw.(map[string]any); ok {
			ids[i] = asString(step["step_id"])
		}
	}
	return ids
}

// stripStepIDsFromProps removes step_id from every step — used on import so
// properties_json stored in state matches what a user would write in config.
func stripStepIDsFromProps(props map[string]any) {
	steps, ok := props["logic_steps"].([]any)
	if !ok {
		return
	}
	for i, raw := range steps {
		if step, ok := raw.(map[string]any); ok {
			delete(step, "step_id")
			steps[i] = step
		}
	}
}

// stepIDsToList converts a []string into a types.List of StringType elements.
func stepIDsToList(ids []string) types.List {
	if len(ids) == 0 {
		v, _ := types.ListValue(types.StringType, []attr.Value{})
		return v
	}
	elems := make([]attr.Value, len(ids))
	for i, id := range ids {
		elems[i] = types.StringValue(id)
	}
	v, _ := types.ListValue(types.StringType, elems)
	return v
}

// listToStepIDs converts a types.List of strings back to []string.
func listToStepIDs(l types.List) []string {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	elems := l.Elements()
	ids := make([]string, len(elems))
	for i, e := range elems {
		if s, ok := e.(types.String); ok {
			ids[i] = s.ValueString()
		}
	}
	return ids
}
