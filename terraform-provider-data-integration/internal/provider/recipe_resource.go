package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*recipeResource)(nil)
	_ resource.ResourceWithConfigure   = (*recipeResource)(nil)
	_ resource.ResourceWithImportState = (*recipeResource)(nil)
)

// NewRecipeResource is the factory registered with the provider.
func NewRecipeResource() resource.Resource { return &recipeResource{} }

type recipeResource struct {
	data *providerData
}

type recipeModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Name          types.String `tfsdk:"name"`
	FilePath      types.String `tfsdk:"file_path"`
	ContentHash   types.String `tfsdk:"content_hash"`
}

func (r *recipeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_recipe"
}

func (r *recipeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Data Integration recipe. Recipes are uploaded as files via multipart/form-data " +
			"in a two-step create (file upload → recipe record). Drift is detected by comparing " +
			"content_hash against the file at file_path; a hash change triggers an update.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Recipe ID (cross_id), assigned by the API.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment this recipe belongs to. Falls back to the provider-level " +
					"environment_id. Changing it forces a new recipe.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Recipe name.",
			},
			"file_path": schema.StringAttribute{
				Required: true,
				Description: "Local path to the recipe file (YAML or JSON). The file is uploaded " +
					"as multipart/form-data. Changing the filename forces a new recipe (S3 key " +
					"drift — the API stores the original filename in the S3 key).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"content_hash": schema.StringAttribute{
				Computed:    true,
				Description: "SHA-256 hash of the uploaded file content. Used for drift detection — the provider recomputes the hash on each read and triggers an update when it changes.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *recipeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *recipeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan recipeModel
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
	plan.EnvironmentID = types.StringValue(envID)

	// TODO: upload file at plan.FilePath via multipart POST /environments/{envID}/recipes/files,
	// then POST /environments/{envID}/recipes with the returned file cross_id.
	resp.Diagnostics.AddError("Not implemented", fmt.Sprintf("%T.Create is not yet implemented", r))
}

func (r *recipeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state recipeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// TODO: GET /environments/{envID}/recipes/{id}, recompute content_hash by
	// fetching the presigned_url and hashing the bytes, remove resource on 404.
	resp.Diagnostics.AddError("Not implemented", fmt.Sprintf("%T.Read is not yet implemented", r))
}

func (r *recipeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan recipeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// TODO: re-upload file via multipart PUT /environments/{envID}/recipes/files/{fileID},
	// then PUT /environments/{envID}/recipes/{id}.
	resp.Diagnostics.AddError("Not implemented", fmt.Sprintf("%T.Update is not yet implemented", r))
}

func (r *recipeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state recipeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// TODO: DELETE /environments/{envID}/recipes/{id}, then DELETE the recipe file.
	resp.Diagnostics.AddError("Not implemented", fmt.Sprintf("%T.Delete is not yet implemented", r))
}

func (r *recipeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
			"Use \"<environment_id>/<recipe_id>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}
