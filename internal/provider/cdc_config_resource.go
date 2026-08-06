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
	_ resource.Resource                = (*cdcConfigResource)(nil)
	_ resource.ResourceWithConfigure   = (*cdcConfigResource)(nil)
	_ resource.ResourceWithImportState = (*cdcConfigResource)(nil)
)

// NewCDCConfigResource is the factory registered with the provider.
func NewCDCConfigResource() resource.Resource { return &cdcConfigResource{} }

type cdcConfigResource struct {
	data *providerData
}

type cdcConfigModel struct {
	ID            types.String         `tfsdk:"id"`
	EnvironmentID types.String         `tfsdk:"environment_id"`
	DataFlowID    types.String         `tfsdk:"data_flow_id"`
	ConfigJSON    jsontypes.Normalized `tfsdk:"config_json"`
}

func (r *cdcConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_data_flow_cdc_config"
}

func (r *cdcConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The CDC offset configuration for a CDC data flow — the source position " +
			"the next run resumes from (mysql binlog, postgres/sqlserver lsn, mongodb resume token, " +
			"oracle scn). Omit config_json to observe the current offset without managing it. " +
			"Set config_json to seed or reset the offset; remove it afterwards to let the offset " +
			"advance freely.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Resource id. Equals data_flow_id (one CDC config per data flow).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment the data flow belongs to. Falls back to the provider-level " +
					"environment_id. Changing it forces a new resource.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"data_flow_id": schema.StringAttribute{
				Required:      true,
				Description:   "cross_id of the CDC data flow this offset belongs to. Changing it forces a new resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"config_json": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				CustomType: jsontypes.NormalizedType{},
				Description: "The current CDC offset as JSON, including a \"datasource_type\" discriminator " +
					"(e.g. {\"datasource_type\":\"mysql\",\"binlog_file\":\"...\",\"binlog_position\":\"...\"}). " +
					"Populated automatically from the API on refresh. Set explicitly only to seed or reset " +
					"the offset; the value is sent to the API wrapped as {\"config\": <this>}.",
			},
		},
	}
}

func (r *cdcConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *cdcConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan cdcConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, &plan, &resp.Diagnostics, "Error creating CDC config")
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *cdcConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state cdcConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.data.client.GetCDCConfig(ctx, state.EnvironmentID.ValueString(), state.DataFlowID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) || errors.Is(err, client.ErrValidation) {
			// Offset not yet materialized — keep whatever is in state.
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading CDC config", err)
		return
	}

	// The API may return {"config": {...}} or just the inner object directly.
	inner, ok := result["config"]
	if !ok {
		inner = result
	}
	b, jsonErr := json.Marshal(inner)
	if jsonErr != nil {
		resp.Diagnostics.AddError("Error reading CDC config", fmt.Sprintf("failed to marshal API response: %s", jsonErr))
		return
	}
	state.ConfigJSON = jsontypes.NewNormalizedValue(string(b))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *cdcConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan cdcConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, &plan, &resp.Diagnostics, "Error updating CDC config")
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *cdcConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state cdcConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.data.client.DeleteCDCConfig(ctx, state.EnvironmentID.ValueString(), state.DataFlowID.ValueString())
	if err != nil {
		// Already gone, or no offset materialized — nothing to delete.
		if errors.Is(err, client.ErrNotFound) || errors.Is(err, client.ErrValidation) {
			return
		}
		addAPIError(&resp.Diagnostics, "Error deleting CDC config", err)
	}
}

func (r *cdcConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	envID, dataFlowID, err := splitImportID(req.ID)
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
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), dataFlowID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("data_flow_id"), dataFlowID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}

// set resolves the environment and, if config_json is set, POSTs the CDC offset.
func (r *cdcConfigResource) set(ctx context.Context, plan *cdcConfigModel, diags *diag.Diagnostics, action string) {
	envID := resolveEnvironmentID(plan.EnvironmentID.ValueString(), r.data)
	if envID == "" {
		diags.AddAttributeError(path.Root("environment_id"), "Missing environment_id",
			"Set environment_id on the resource or environment_id on the provider.")
		return
	}
	plan.ID = plan.DataFlowID
	plan.EnvironmentID = types.StringValue(envID)

	if plan.ConfigJSON.IsNull() || plan.ConfigJSON.IsUnknown() {
		return
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(plan.ConfigJSON.ValueString()), &config); err != nil {
		diags.AddAttributeError(path.Root("config_json"), "Invalid config_json",
			fmt.Sprintf("config_json must be a JSON object: %s", err))
		return
	}

	body := map[string]any{"config": config}
	if err := r.data.client.SetCDCConfig(ctx, envID, plan.DataFlowID.ValueString(), body); err != nil {
		addAPIError(diags, action, err)
	}
}
