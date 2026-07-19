package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
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
	GroupID        types.String         `tfsdk:"group_id"`
	Activate       types.Bool           `tfsdk:"activate"`
}

func (r *dataFlowResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_data_flow"
}

func (r *dataFlowResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Data Integration data flow (the API calls this a \"river\"). The flow " +
			"definition is supplied as JSON via properties_json; this keeps the resource forward-" +
			"compatible with the full river schema without re-modelling every field.",
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
				Description: "River kind. Defaults to \"main_river\".",
			},
			"type": schema.StringAttribute{
				Required:    true,
				Description: "River type. One of: \"source_to_target\", \"logic\", \"actions\", \"connector_executor\".",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Description. Stored under the API's metadata.description (a top-level description is rejected).",
			},
			"properties_json": schema.StringAttribute{
				Required:   true,
				CustomType: jsontypes.NormalizedType{},
				Description: "The river properties object as JSON — must include a properties_type " +
					"discriminator and (for logic flows) a non-empty logic_steps array. This value " +
					"is config-authoritative: the API enriches it on write (logic_steps gain " +
					"step_id/is_enabled/…), so the provider keeps your configured value and does not " +
					"refresh it from the API. Drift inside this blob is therefore not detected.",
			},
			"settings_json": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				CustomType: jsontypes.NormalizedType{},
				Default:    stringdefault.StaticString("{}"),
				Description: "The river settings object as JSON. Defaults to an empty object. " +
					"Config-authoritative like properties_json (the API adds a notification block " +
					"on write); the provider does not refresh it from the API.",
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
				Default:  booldefault.StaticBool(false),
				Description: "Whether to activate (enable) the data flow after create or update. " +
					"When true the provider runs: disable (if already active) → update → activate. " +
					"The disable+update step initialises the fire-service task entry that the " +
					"activate_river API requires — rivers created via the API lack this entry until " +
					"their first PUT, so setting activate = true here is the correct way to enable a " +
					"freshly-created data flow.",
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

	body, ok := r.buildBody(plan, &resp.Diagnostics)
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

	if plan.Activate.ValueBool() {
		// A PUT after POST initialises the fire-service task entry that
		// activate_river requires. Without this step, activation always fails
		// with RVR-ACTIVATE-500 for API-created rivers.
		if _, err2 := r.data.client.UpdateDataFlow(ctx, envID, plan.ID.ValueString(), body); err2 != nil {
			addAPIError(&resp.Diagnostics, "Error initialising data flow before activation", err2)
			return
		}
		resp.Diagnostics.Append(r.activateFlow(ctx, envID, plan.ID.ValueString(), dataFlowOpTimeout)...)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *dataFlowResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state dataFlowModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envID := plan.EnvironmentID.ValueString()

	body, ok := r.buildBody(plan, &resp.Diagnostics)
	if !ok {
		return
	}

	// Detect transition to CDC (log-based) extract method. When switching to log,
	// the API validator requires a scheduler in the PUT body. We fetch the existing
	// schedulers from the GET response and inject them so the validator passes.
	switchingToCDC := isCDCFlow(plan.PropertiesJSON.ValueString()) &&
		!isCDCFlow(state.PropertiesJSON.ValueString())

	if switchingToCDC {
		current, err := r.data.client.GetDataFlow(ctx, envID, plan.ID.ValueString())
		if err != nil {
			addAPIError(&resp.Diagnostics, "Error fetching data flow schedulers for CDC transition", err)
			return
		}
		if schedulers, ok := current["schedulers"]; ok {
			body["schedulers"] = schedulers
		}
	}

	if plan.Activate.ValueBool() {
		// Disable before editing — the API requires the river to be inactive
		// during a PUT when it was previously active.
		if opID, err2 := r.data.client.DisableDataFlow(ctx, envID, plan.ID.ValueString()); err2 != nil {
			addAPIError(&resp.Diagnostics, "Error disabling data flow before update", err2)
			return
		} else if opID != "" {
			if !r.waitForOp(ctx, envID, opID, &resp.Diagnostics) {
				return
			}
		}
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

	// After switching to CDC, enable_cdc must be called before the river can run.
	// This is a one-time operation that sets ENABLE_LOG=true on the river.
	if switchingToCDC {
		resp.Diagnostics.Append(r.enableCDC(ctx, envID, plan.ID.ValueString())...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	if plan.Activate.ValueBool() {
		// CDC rivers take longer to activate after enable_cdc — use the extended timeout.
		activateTimeout := dataFlowOpTimeout
		if switchingToCDC {
			activateTimeout = cdcEnableOpTimeout
		}
		resp.Diagnostics.Append(r.activateFlow(ctx, envID, plan.ID.ValueString(), activateTimeout)...)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dataFlowResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dataFlowModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.DeleteDataFlow(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString()); err != nil {
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
// Pass timeout=cdcEnableOpTimeout when activating immediately after enable_cdc,
// since CDC rivers take longer to reach ACTIVE state.
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

// enableCDC calls the enable_cdc endpoint and polls the async operation.
// Required after switching a river to extract_method=log before it can run.
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

func (r *dataFlowResource) buildBody(plan dataFlowModel, diags *diag.Diagnostics) (map[string]any, bool) {
	props, ok := decodeJSONObject(plan.PropertiesJSON, path.Root("properties_json"), diags)
	if !ok {
		return nil, false
	}
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

	// description <- metadata.description
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

	// properties_json / settings_json are config-authoritative JSON passthrough.
	// The API enriches them on write (logic_steps gain step_id/is_enabled/…,
	// settings gains a notification block), so echoing the server value back
	// would (a) break Terraform's plan==apply consistency contract and (b)
	// produce a perpetual diff on refresh. We therefore preserve the configured
	// value and only populate from the server when it is absent — i.e. on
	// import, where there is no prior config to honor.
	if m.PropertiesJSON.IsNull() || m.PropertiesJSON.IsUnknown() {
		if props, ok := api["properties"].(map[string]any); ok {
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
