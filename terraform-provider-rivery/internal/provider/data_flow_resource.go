package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/boomi/terraform-provider-rivery/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("logic"),
				Description: "River type (e.g. \"logic\", \"src_to_target\"). Defaults to \"logic\".",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Description. Stored under the API's metadata.description (a top-level description is rejected).",
			},
			"properties_json": schema.StringAttribute{
				Required:   true,
				CustomType: jsontypes.NormalizedType{},
				Description: "The river properties object as JSON — must include a properties_type " +
					"discriminator and (for logic flows) a non-empty logic_steps array. Compared " +
					"semantically, so formatting differences do not produce diffs.",
			},
			"settings_json": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				CustomType:  jsontypes.NormalizedType{},
				Default:     stringdefault.StaticString("{}"),
				Description: "The river settings object as JSON. Defaults to an empty object.",
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
		resp.Diagnostics.AddError("Error creating data flow", err.Error())
		return
	}
	resp.Diagnostics.Append(r.apply(created, envID, &plan)...)
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
		resp.Diagnostics.AddError("Error reading data flow", err.Error())
		return
	}
	resp.Diagnostics.Append(r.apply(df, state.EnvironmentID.ValueString(), &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *dataFlowResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dataFlowModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envID := plan.EnvironmentID.ValueString()

	body, ok := r.buildBody(plan, &resp.Diagnostics)
	if !ok {
		return
	}

	updated, err := r.data.client.UpdateDataFlow(ctx, envID, plan.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Error updating data flow", err.Error())
		return
	}
	resp.Diagnostics.Append(r.apply(updated, envID, &plan)...)
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
		resp.Diagnostics.AddError("Error deleting data flow", err.Error())
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

	// properties / settings round-trip as normalized JSON
	if props, ok := api["properties"].(map[string]any); ok {
		if raw, err := json.Marshal(props); err == nil {
			m.PropertiesJSON = jsontypes.NewNormalizedValue(string(raw))
		} else {
			diags.AddError("Error encoding properties", err.Error())
		}
	}
	if settings, ok := api["settings"].(map[string]any); ok {
		if raw, err := json.Marshal(settings); err == nil {
			m.SettingsJSON = jsontypes.NewNormalizedValue(string(raw))
		}
	} else if m.SettingsJSON.IsUnknown() {
		m.SettingsJSON = jsontypes.NewNormalizedValue("{}")
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
