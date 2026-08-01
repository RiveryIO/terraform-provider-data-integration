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
	_ resource.Resource                = (*blueprintFileResource)(nil)
	_ resource.ResourceWithConfigure   = (*blueprintFileResource)(nil)
	_ resource.ResourceWithImportState = (*blueprintFileResource)(nil)
)

// NewBlueprintFileResource is the factory registered with the provider.
func NewBlueprintFileResource() resource.Resource { return &blueprintFileResource{} }

type blueprintFileResource struct {
	data *providerData
}

type blueprintFileModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Filename      types.String `tfsdk:"filename"`
	// Content is write-only: stored in state from plan, never read back from
	// the API (the API returns a short-lived presigned S3 URL, not the YAML
	// text). Drift detection covers existence only, not content changes.
	Content types.String `tfsdk:"content"`
}

func (r *blueprintFileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_blueprint_file"
}

func (r *blueprintFileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The YAML content backing a Blueprint (the customer-facing term; the API/code " +
			"term is \"recipe\"). Reference this resource's id as a blueprint_resource's file_cross_id. " +
			"Changing filename or content updates this resource in place (its id is stable) — the API " +
			"supports PUT unlike the logicode file API.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The cross_id assigned by the API. Reference this in blueprint_resource.file_cross_id.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"environment_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Environment this file belongs to. Falls back to the provider-level environment_id. Changing it forces a new file.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"filename": schema.StringAttribute{
				Required:    true,
				Description: "Display name for the blueprint YAML file (e.g. \"github_commits.yaml\").",
			},
			"content": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Blueprint YAML content. Stored in state; never read back from the API.",
			},
		},
	}
}

func (r *blueprintFileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *blueprintFileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan blueprintFileModel
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

	created, err := r.data.client.CreateBlueprintFile(ctx, envID, plan.Filename.ValueString(), plan.Content.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating blueprint file", err)
		return
	}

	plan.ID = types.StringValue(created.CrossID)
	plan.EnvironmentID = types.StringValue(envID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *blueprintFileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state blueprintFileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.data.client.GetBlueprintFile(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx) // drift: file deleted out-of-band
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading blueprint file", err)
		return
	}
	// content is config-authoritative: keep whatever is in state, don't try to
	// read from the API (it exposes a presigned URL, not the YAML text).
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *blueprintFileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan blueprintFileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.data.client.UpdateBlueprintFile(ctx, plan.EnvironmentID.ValueString(), plan.ID.ValueString(),
		plan.Filename.ValueString(), plan.Content.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating blueprint file", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *blueprintFileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state blueprintFileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.DeleteBlueprintFile(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return // already gone
		}
		addAPIError(&resp.Diagnostics, "Error deleting blueprint file", err)
	}
}

// ImportState accepts "<environment_id>/<cross_id>" or a bare cross_id.
func (r *blueprintFileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	envID, fileID, err := splitImportID(req.ID)
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
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), fileID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
	// content cannot be recovered from the API; user must set it in config.
}
