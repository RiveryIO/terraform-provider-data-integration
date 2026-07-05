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
	_ resource.Resource                = (*environmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*environmentResource)(nil)
	_ resource.ResourceWithImportState = (*environmentResource)(nil)
)

// NewEnvironmentResource is the factory registered with the provider.
func NewEnvironmentResource() resource.Resource { return &environmentResource{} }

type environmentResource struct {
	data *providerData
}

type environmentModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

func (r *environmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (r *environmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Data Integration environment. Environments are account-scoped and " +
			"group connections and data flows.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Environment ID, assigned by the API.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Environment name.",
			},
			"description": schema.StringAttribute{
				Optional:    true,
				Description: "Free-text description.",
			},
		},
	}
}

func (r *environmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *environmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := map[string]any{"name": plan.Name.ValueString()}
	if !plan.Description.IsNull() {
		body["description"] = plan.Description.ValueString()
	}

	created, err := r.data.client.CreateEnvironment(ctx, body)
	if err != nil {
		if errors.Is(err, client.ErrConflict) {
			var apiErr *client.APIError
			if errors.As(err, &apiErr) && apiErr.ConflictID != "" {
				resp.Diagnostics.AddError(
					"Environment already exists",
					"An environment named \""+plan.Name.ValueString()+"\" already exists "+
						"(id: "+apiErr.ConflictID+"). "+
						"To manage it with Terraform run:\n\n"+
						"  terraform import boomi_environment."+
						"<resource_label> "+apiErr.ConflictID,
				)
				return
			}
		}
		addAPIError(&resp.Diagnostics, "Error creating environment", err)
		return
	}
	r.apply(created, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *environmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	env, err := r.data.client.GetEnvironment(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx) // drift: deleted out-of-band
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading environment", err)
		return
	}
	// Environment delete is a server-side soft-delete: the record stays readable
	// with is_deleted=true. Treat that as gone so out-of-band deletes are detected
	// and we never re-issue a DELETE against an already-deleted env (which the API
	// answers with a 500 rather than a 404).
	if isTrue(env["is_deleted"]) {
		resp.State.RemoveResource(ctx)
		return
	}
	r.apply(env, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *environmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	patch := map[string]any{"name": plan.Name.ValueString()}
	if plan.Description.IsNull() {
		patch["description"] = nil
	} else {
		patch["description"] = plan.Description.ValueString()
	}

	updated, err := r.data.client.UpdateEnvironment(ctx, plan.ID.ValueString(), patch)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating environment", err)
		return
	}
	r.apply(updated, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *environmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read before delete: the API returns 500 when DELETE is called on an environment
	// that is already soft-deleted (is_deleted=true) or has deletion queued
	// (is_delete_lock=true). Checking first avoids the broken endpoint entirely.
	existing, err := r.data.client.GetEnvironment(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return // already gone
		}
		addAPIError(&resp.Diagnostics, "Error reading environment before delete", err)
		return
	}
	if isTrue(existing["is_deleted"]) || isTrue(existing["is_delete_lock"]) {
		return // soft-delete already in progress or complete
	}

	if err := r.data.client.DeleteEnvironment(ctx, state.ID.ValueString()); err != nil {
		// 404 = deleted between the read and the delete call.
		// 409 = deletion was queued asynchronously between the read and the delete call.
		if errors.Is(err, client.ErrNotFound) || errors.Is(err, client.ErrConflict) {
			return
		}
		addAPIError(&resp.Diagnostics, "Error deleting environment", err)
	}
}

func (r *environmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// apply maps an API response onto the model, normalizing read shape so an
// imported resource plans clean.
func (r *environmentResource) apply(api map[string]any, m *environmentModel) {
	m.ID = types.StringValue(asString(api["id"]))
	m.Name = types.StringValue(asString(api["name"]))
	if desc := asString(api["description"]); desc != "" {
		m.Description = types.StringValue(desc)
	} else if m.Description.IsUnknown() {
		m.Description = types.StringNull()
	}
}
