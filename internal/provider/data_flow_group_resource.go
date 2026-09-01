package provider

import (
	"context"
	"errors"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
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
	_ resource.Resource                = (*dataFlowGroupResource)(nil)
	_ resource.ResourceWithConfigure   = (*dataFlowGroupResource)(nil)
	_ resource.ResourceWithImportState = (*dataFlowGroupResource)(nil)
)

// NewDataFlowGroupResource is the factory registered with the provider.
func NewDataFlowGroupResource() resource.Resource { return &dataFlowGroupResource{} }

type dataFlowGroupResource struct {
	data *providerData
}

type dataFlowGroupModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Name          types.String `tfsdk:"name"`
	Color         types.String `tfsdk:"color"`
	Icon          types.String `tfsdk:"icon"`
	IsDefault     types.Bool   `tfsdk:"is_default"`
}

func (r *dataFlowGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_data_flow_group"
}

func (r *dataFlowGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A data flow group (folder) — purely organizational, used to file data flows " +
			"into folders in the console UI. Has nothing to do with permissions or access control.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Cross ID of the group, assigned by the API.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment this group belongs to. Falls back to the " +
					"provider-level environment_id. Changing it forces a new group.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Display name of the group.",
			},
			"color": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("#a0a8ff"),
				Description: "Display color of the group, as a hex or named color.",
			},
			"icon": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("folder"),
				Description: "Icon identifier for the group.",
			},
			"is_default": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				Description: "Whether this is the environment's default group — rivers whose " +
					"group is deleted are moved here. Only one group per environment can be " +
					"default. Setting this true on one group implicitly unsets it on whichever " +
					"group was previously default (which will show as drift there if that group " +
					"is also managed by this provider). The API rejects explicitly setting this " +
					"to false on the group that is currently default — make a different group " +
					"default instead.",
			},
		},
	}
}

func (r *dataFlowGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *dataFlowGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dataFlowGroupModel
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

	body := map[string]any{
		"name":       plan.Name.ValueString(),
		"color":      plan.Color.ValueString(),
		"icon":       plan.Icon.ValueString(),
		"is_default": plan.IsDefault.ValueBool(),
	}

	created, err := r.data.client.CreateDataFlowGroup(ctx, envID, body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating data flow group", err)
		return
	}
	r.apply(created, envID, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dataFlowGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dataFlowGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.data.client.GetDataFlowGroup(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx) // drift: deleted out-of-band
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading data flow group", err)
		return
	}
	r.apply(group, state.EnvironmentID.ValueString(), &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *dataFlowGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dataFlowGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The API's patch input has no partial-update semantics for these three —
	// all must be resent every call, even when only one of them changed.
	body := map[string]any{
		"name":       plan.Name.ValueString(),
		"color":      plan.Color.ValueString(),
		"icon":       plan.Icon.ValueString(),
		"is_default": plan.IsDefault.ValueBool(),
	}

	updated, err := r.data.client.UpdateDataFlowGroup(ctx, plan.EnvironmentID.ValueString(), plan.ID.ValueString(), body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating data flow group", err)
		return
	}
	r.apply(updated, plan.EnvironmentID.ValueString(), &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dataFlowGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dataFlowGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.DeleteDataFlowGroup(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return // already gone
		}
		addAPIError(&resp.Diagnostics, "Error deleting data flow group", err)
	}
}

func (r *dataFlowGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
			"Use \"<environment_id>/<group_id>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}

// apply maps an API response onto the model.
func (r *dataFlowGroupResource) apply(g client.DataFlowGroup, envID string, m *dataFlowGroupModel) {
	m.ID = types.StringValue(g.ID)
	m.EnvironmentID = types.StringValue(envID)
	m.Name = types.StringValue(g.Name)
	m.Color = types.StringValue(g.Color)
	m.Icon = types.StringValue(g.Icon)
	m.IsDefault = types.BoolValue(g.IsDefault)
}
