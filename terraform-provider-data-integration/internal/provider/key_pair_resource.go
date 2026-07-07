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
	_ resource.Resource                = (*keyPairResource)(nil)
	_ resource.ResourceWithConfigure   = (*keyPairResource)(nil)
	_ resource.ResourceWithImportState = (*keyPairResource)(nil)
)

// NewKeyPairResource is the factory registered with the provider.
func NewKeyPairResource() resource.Resource { return &keyPairResource{} }

type keyPairResource struct {
	data *providerData
}

type keyPairModel struct {
	ID         types.String `tfsdk:"id"`
	EnvID      types.String `tfsdk:"env_id"`
	Name       types.String `tfsdk:"name"`
	PublicKey  types.String `tfsdk:"public_key"`
	PrivateKey types.String `tfsdk:"private_key"`
}

func (r *keyPairResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key_pair"
}

func (r *keyPairResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An RSA key pair for a Data Integration account. The API has no update " +
			"endpoint — all attributes are RequiresReplace. The private key is returned only " +
			"once on create and is never returned by subsequent reads.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Key pair ID, assigned by the API.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"env_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment this key pair belongs to. Falls back to the provider-level " +
					"environment_id. Changing it forces a new key pair.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Key pair name. Changing it forces a new key pair (no update endpoint).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"public_key": schema.StringAttribute{
				Computed:    true,
				Description: "RSA public key returned by the API after create.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"private_key": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "RSA private key. Returned only once on create; never returned by reads. Stored in state as sensitive.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *keyPairResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *keyPairResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan keyPairModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	envID := resolveEnvironmentID(plan.EnvID.ValueString(), r.data)
	if envID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("env_id"), "Missing env_id",
			"Set env_id on the resource or environment_id on the provider.")
		return
	}
	plan.EnvID = types.StringValue(envID)

	// TODO: POST /environments/{envID}/key_pairs with name.
	resp.Diagnostics.AddError("Not implemented", fmt.Sprintf("%T.Create is not yet implemented", r))
}

func (r *keyPairResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state keyPairModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// TODO: GET /environments/{envID}/key_pairs/{id}, remove resource on 404.
	resp.Diagnostics.AddError("Not implemented", fmt.Sprintf("%T.Read is not yet implemented", r))
}

func (r *keyPairResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	// All attributes are RequiresReplace — Update is never called.
	resp.Diagnostics.AddError("Not implemented", fmt.Sprintf("%T.Update is not yet implemented", r))
}

func (r *keyPairResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state keyPairModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// TODO: DELETE /environments/{envID}/key_pairs/{id}, treat 404 as success.
	resp.Diagnostics.AddError("Not implemented", fmt.Sprintf("%T.Delete is not yet implemented", r))
}

func (r *keyPairResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	envID, id, err := splitImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	if envID == "" {
		envID = resolveEnvironmentID("", r.data)
	}
	if envID == "" {
		resp.Diagnostics.AddError("Missing env_id for import",
			"Use \"<env_id>/<key_pair_id>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("env_id"), envID)...)
}
