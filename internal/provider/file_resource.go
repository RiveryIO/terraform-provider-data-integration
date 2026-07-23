package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = (*fileResource)(nil)

// NewFileResource returns a factory for boomi_data_integration_file.
func NewFileResource() resource.Resource { return &fileResource{} }

type fileResource struct{ data *providerData }

type fileModel struct {
	ID             types.String `tfsdk:"id"`
	EnvironmentID  types.String `tfsdk:"environment_id"`
	ConnectionType types.String `tfsdk:"connection_type"`
	LocalPath      types.String `tfsdk:"local_path"`
	RemotePath     types.String `tfsdk:"remote_path"`
}

func (r *fileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (r *fileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *fileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Uploads a local file to the platform's shared file storage and exposes the " +
			"resulting remote path. The remote path can be referenced in a connection's " +
			"parameters_json (e.g. key_file_path for Snowflake key-pair auth). " +
			"Changing local_path or connection_type forces a new upload.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"environment_id": schema.StringAttribute{
				Required:    true,
				Description: "Environment to upload the file into.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"connection_type": schema.StringAttribute{
				Required: true,
				Description: "Connection type that owns this file (e.g. \"snowflake\", \"bq_src\"). " +
					"Determines the upload endpoint.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"local_path": schema.StringAttribute{
				Required:    true,
				Description: "Absolute path to the file on the local machine. Changing this value replaces the resource (re-uploads).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"remote_path": schema.StringAttribute{
				Computed:    true,
				Description: "Platform path returned after upload — use this in parameters_json (e.g. key_file_path).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *fileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	envID := resolveEnvironmentID(plan.EnvironmentID.ValueString(), r.data)
	if envID == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("environment_id"),
			"Missing environment_id",
			"Set environment_id on the resource or environment_id on the provider.",
		)
		return
	}

	remotePath, err := r.data.client.UploadConnectionFile(
		ctx, envID,
		plan.ConnectionType.ValueString(),
		plan.LocalPath.ValueString(),
	)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error uploading file", err)
		return
	}

	plan.RemotePath = types.StringValue(remotePath)
	plan.ID = types.StringValue(remotePath)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *fileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// The platform has no GET endpoint for uploaded files — trust state.
	var state fileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *fileResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	// All mutable attributes use RequiresReplace — Update is never called.
}

func (r *fileResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// The platform has no DELETE endpoint for uploaded files — removing from state is sufficient.
}
