package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*connectionResource)(nil)
	_ resource.ResourceWithConfigure   = (*connectionResource)(nil)
	_ resource.ResourceWithImportState = (*connectionResource)(nil)
)

// NewConnectionResource is the factory registered with the provider.
func NewConnectionResource() resource.Resource { return &connectionResource{} }

type connectionResource struct {
	data *providerData
}

type connectionModel struct {
	ID             types.String         `tfsdk:"id"`
	EnvironmentID  types.String         `tfsdk:"environment_id"`
	Name           types.String         `tfsdk:"name"`
	Type           types.String         `tfsdk:"type"`
	ParametersJSON jsontypes.Normalized `tfsdk:"parameters_json"`
}

func (r *connectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection"
}

func (r *connectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Data Integration connection to a data source or target.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Connection ID, assigned by the API.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment this connection belongs to. Falls back to the " +
					"provider-level environment_id. Changing it forces a new connection.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Connection name.",
			},
			"type": schema.StringAttribute{
				Required: true,
				Description: "Connection type identifier (e.g. \"snowflake\", \"postgres\"). " +
					"Changing it forces a new connection.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"parameters_json": schema.StringAttribute{
				Optional:   true,
				Sensitive:  true,
				CustomType: jsontypes.NormalizedType{},
				Description: "Connection-type-specific parameters as a JSON object, including " +
					"credentials. Treated as write-only: the API omits secrets on read, so this " +
					"value is preserved from configuration and never refreshed from the API.",
			},
		},
	}
}

func (r *connectionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *connectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectionModel
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

	// The connections API speaks connection_name / connection_type (not the
	// generic name/type used by rivers and environments).
	body := map[string]any{"connection_name": plan.Name.ValueString(), "connection_type": plan.Type.ValueString()}
	if params, ok := r.decodeParams(plan, &resp.Diagnostics); ok {
		mergeParams(body, params)
	} else if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.data.client.CreateConnection(ctx, envID, body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating connection", err)
		return
	}
	r.apply(created, envID, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state connectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn, err := r.data.client.GetConnection(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading connection", err)
		return
	}
	// apply preserves parameters_json from prior state — never overwritten from
	// the API, which omits credentials on read.
	r.apply(conn, state.EnvironmentID.ValueString(), &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *connectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envID := plan.EnvironmentID.ValueString()

	patch := map[string]any{"connection_name": plan.Name.ValueString(), "connection_type": plan.Type.ValueString()}
	if params, ok := r.decodeParams(plan, &resp.Diagnostics); ok {
		mergeParams(patch, params)
	} else if resp.Diagnostics.HasError() {
		return
	}

	updated, err := r.data.client.UpdateConnection(ctx, envID, plan.ID.ValueString(), patch)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating connection", err)
		return
	}
	r.apply(updated, envID, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state connectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.DeleteConnection(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		addAPIError(&resp.Diagnostics, "Error deleting connection", err)
	}
}

func (r *connectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
			"Use \"<environment_id>/<connection_id>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}

// apply maps an API response onto the model. parameters_json is intentionally
// left untouched (write-only secret handling).
func (r *connectionResource) apply(api map[string]any, envID string, m *connectionModel) {
	m.ID = types.StringValue(asString(api["id"]))
	m.EnvironmentID = types.StringValue(envID)
	m.Name = types.StringValue(asString(api["name"]))
	if t := asString(api["type"]); t != "" {
		m.Type = types.StringValue(t)
	}
}

// decodeParams parses parameters_json into a map. Returns (nil,false) when
// unset; appends a diagnostic and returns (nil,false) on malformed JSON.
func (r *connectionResource) decodeParams(m connectionModel, diags *diag.Diagnostics) (map[string]any, bool) {
	if m.ParametersJSON.IsNull() || m.ParametersJSON.IsUnknown() {
		return nil, false
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(m.ParametersJSON.ValueString()), &params); err != nil {
		diags.AddAttributeError(path.Root("parameters_json"), "Invalid parameters_json",
			fmt.Sprintf("parameters_json must be a JSON object: %s", err))
		return nil, false
	}
	return params, true
}

// mergeParams copies type-specific params into the request body without
// clobbering the reserved top-level keys.
func mergeParams(body, params map[string]any) {
	for k, v := range params {
		if k == "name" || k == "type" {
			continue
		}
		body[k] = v
	}
}
