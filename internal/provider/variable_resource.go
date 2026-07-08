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
	_ resource.Resource                = (*variableResource)(nil)
	_ resource.ResourceWithConfigure   = (*variableResource)(nil)
	_ resource.ResourceWithImportState = (*variableResource)(nil)
)

// NewVariableResource is the factory registered with the provider.
func NewVariableResource() resource.Resource { return &variableResource{} }

type variableResource struct {
	data *providerData
}

type variableModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Key           types.String `tfsdk:"key"`
	Value         types.String `tfsdk:"value"`
}

func (r *variableResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_variable"
}

func (r *variableResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Data Integration environment variable. Variables are an environment-scoped " +
			"key/value collection; each key is managed as its own resource (writes merge, so sibling " +
			"keys are left intact).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Resource id. Equals the variable key (variables are keyed by name).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment this variable belongs to. Falls back to the provider-level " +
					"environment_id. Changing it forces a new variable.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"key": schema.StringAttribute{
				Required: true,
				Description: "Variable key. Must not contain \".\" or \"$\". Changing it forces a new " +
					"variable.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"value": schema.StringAttribute{
				Required:    true,
				Description: "Variable value.",
			},
		},
	}
}

func (r *variableResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *variableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan variableModel
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

	if err := r.data.client.PutVariable(ctx, envID, plan.Key.ValueString(), plan.Value.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Error creating variable", err)
		return
	}
	plan.ID = plan.Key
	plan.EnvironmentID = types.StringValue(envID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *variableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state variableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vars, err := r.data.client.ListVariables(ctx, state.EnvironmentID.ValueString())
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error reading variable", err)
		return
	}
	v, ok := vars[state.Key.ValueString()]
	if !ok {
		resp.State.RemoveResource(ctx) // drift: deleted out-of-band
		return
	}
	state.ID = state.Key
	state.Value = types.StringValue(asString(v))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *variableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan variableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envID := plan.EnvironmentID.ValueString()

	if err := r.data.client.PutVariable(ctx, envID, plan.Key.ValueString(), plan.Value.ValueString()); err != nil {
		addAPIError(&resp.Diagnostics, "Error updating variable", err)
		return
	}
	plan.ID = plan.Key
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *variableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state variableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.DeleteVariable(ctx, state.EnvironmentID.ValueString(), state.Key.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return // already gone
		}
		addAPIError(&resp.Diagnostics, "Error deleting variable", err)
	}
}

// ImportState accepts "<environment_id>/<key>" (or a bare key when a
// provider-level environment_id is set).
func (r *variableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	envID, key, err := splitImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	if envID == "" {
		envID = resolveEnvironmentID("", r.data)
	}
	if envID == "" {
		resp.Diagnostics.AddError("Missing environment_id for import",
			"Use \"<environment_id>/<key>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), key)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), key)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}
