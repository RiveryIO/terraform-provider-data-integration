package provider

import (
	"context"
	"errors"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*blueprintResource)(nil)
	_ resource.ResourceWithConfigure   = (*blueprintResource)(nil)
	_ resource.ResourceWithImportState = (*blueprintResource)(nil)
)

// NewBlueprintResource is the factory registered with the provider.
func NewBlueprintResource() resource.Resource { return &blueprintResource{} }

type blueprintResource struct {
	data *providerData
}

type blueprintModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Name          types.String `tfsdk:"name"`
	FileCrossID   types.String `tfsdk:"file_cross_id"`
	Description   types.String `tfsdk:"description"`
}

func (r *blueprintResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_blueprint"
}

func (r *blueprintResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Blueprint (the customer-facing term; the API/code term is \"recipe\"). " +
			"A blueprint is a named pointer to a blueprint_file's content, used as a data_flow's " +
			"source (source.name = \"blueprint\", source.additional_settings.recipe_id = this resource's id).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The cross_id assigned by the API. Reference this as a data_flow source's recipe_id.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"environment_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Environment this blueprint belongs to. Falls back to the provider-level environment_id. Changing it forces a new blueprint.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Blueprint display name.",
			},
			"file_cross_id": schema.StringAttribute{
				Required:    true,
				Description: "The blueprint_file resource's id (cross_id) this blueprint points at. Updating this repoints the blueprint at new YAML content without changing the blueprint's own id.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Free-text description.",
			},
		},
	}
}

func (r *blueprintResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *blueprintResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan blueprintModel
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
		"name":          plan.Name.ValueString(),
		"file_cross_id": plan.FileCrossID.ValueString(),
	}
	if !plan.Description.IsNull() {
		body["description"] = plan.Description.ValueString()
	}

	created, err := r.data.client.CreateBlueprint(ctx, envID, body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating blueprint", err)
		return
	}
	plan.EnvironmentID = types.StringValue(envID)
	r.apply(created, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *blueprintResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state blueprintModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	got, err := r.data.client.GetBlueprint(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx) // drift: deleted out-of-band
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading blueprint", err)
		return
	}
	r.apply(got, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *blueprintResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan blueprintModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	patch := map[string]any{
		"name":          plan.Name.ValueString(),
		"file_cross_id": plan.FileCrossID.ValueString(),
	}
	if plan.Description.IsNull() {
		patch["description"] = nil
	} else {
		patch["description"] = plan.Description.ValueString()
	}

	updated, err := r.data.client.UpdateBlueprint(ctx, plan.EnvironmentID.ValueString(), plan.ID.ValueString(), patch)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating blueprint", err)
		return
	}
	r.apply(updated, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *blueprintResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state blueprintModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.DeleteBlueprint(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return // already gone
		}
		addAPIError(&resp.Diagnostics, "Error deleting blueprint", err)
	}
}

// ImportState accepts "<environment_id>/<cross_id>" or a bare cross_id.
func (r *blueprintResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
			"Use \"<environment_id>/<cross_id>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}

// apply maps an API response onto the model, normalizing read shape so an
// imported resource plans clean.
func (r *blueprintResource) apply(api map[string]any, m *blueprintModel) {
	m.ID = types.StringValue(asString(api["cross_id"]))
	m.Name = types.StringValue(asString(api["name"]))
	m.FileCrossID = types.StringValue(asString(api["file_cross_id"]))
	if desc := asString(api["description"]); desc != "" {
		m.Description = types.StringValue(desc)
	} else if m.Description.IsUnknown() {
		m.Description = types.StringNull()
	}
}
