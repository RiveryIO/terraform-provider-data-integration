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
	_ resource.Resource                = (*logicodeFileResource)(nil)
	_ resource.ResourceWithConfigure   = (*logicodeFileResource)(nil)
	_ resource.ResourceWithImportState = (*logicodeFileResource)(nil)
)

// NewLogicodeFileResource is the factory registered with the provider.
func NewLogicodeFileResource() resource.Resource { return &logicodeFileResource{} }

type logicodeFileResource struct {
	data *providerData
}

type logicodeFileModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Filename      types.String `tfsdk:"filename"`
	// Content is write-only: stored in state from plan, never read back from the
	// API (presigned GET URLs expire in 60s; drift detection on S3 content is
	// unreliable). Any change forces a new file (and a new file_id).
	Content types.String `tfsdk:"content"`
}

func (r *logicodeFileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_logicode_file"
}

func (r *logicodeFileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A logicode (Python) script file for use in logic data flow logicode steps. " +
			"The API is create+read only — DELETE and PUT both return 405. " +
			"Any change to filename or content forces a new resource (new file_id). " +
			"On destroy, TF removes the resource from state only; the file remains in S3.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "The file_id assigned by the API. Reference this in data_flow properties_json.",
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
				Description: "Display name for the script file (e.g. \"my_script.py\"). Changing it forces a new file.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"content": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Python script content. Stored in state; never read back from the API. Changing it forces a new file.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *logicodeFileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *logicodeFileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan logicodeFileModel
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

	// Step 1: register the file, get file_id + presigned PUT URL.
	created, err := r.data.client.CreateLogicodeFile(ctx, envID, plan.Filename.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating logicode file", err)
		return
	}

	// Step 2: upload script content to the presigned S3 URL.
	if err := r.data.client.UploadLogicodeContent(ctx, created.URL, plan.Content.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Error uploading logicode content", err)
		return
	}

	plan.ID = types.StringValue(created.FileID)
	plan.EnvironmentID = types.StringValue(envID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *logicodeFileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state logicodeFileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.data.client.GetLogicodeFile(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx) // drift: file deleted out-of-band
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading logicode file", err)
		return
	}
	// content is config-authoritative: keep whatever is in state, don't try to
	// read from the expiring presigned URL.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never called: all attributes are RequiresReplace.
func (r *logicodeFileResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

// Delete removes the resource from state. The logicode file API has no DELETE
// endpoint (405), so the file remains in S3 after destroy.
func (r *logicodeFileResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// ImportState accepts "<environment_id>/<file_id>" or a bare file_id.
func (r *logicodeFileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
			"Use \"<environment_id>/<file_id>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), fileID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
	// filename and content cannot be recovered from the API; user must set them in config.
}
