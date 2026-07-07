package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource              = (*dataFlowRunResource)(nil)
	_ resource.ResourceWithConfigure = (*dataFlowRunResource)(nil)
)

// activationTimeout bounds how long Create waits for an asynchronous
// activate_river operation to finish before triggering the run.
const activationTimeout = 2 * time.Minute

// NewDataFlowRunResource is the factory registered with the provider.
func NewDataFlowRunResource() resource.Resource { return &dataFlowRunResource{} }

type dataFlowRunResource struct {
	data *providerData
}

type dataFlowRunModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	DataFlowID    types.String `tfsdk:"data_flow_id"`
	Activate      types.Bool   `tfsdk:"activate"`
	Triggers      types.Map    `tfsdk:"triggers"`
	RunID         types.String `tfsdk:"run_id"`
	RunGroupID    types.String `tfsdk:"run_group_id"`
}

func (r *dataFlowRunResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_data_flow_run"
}

func (r *dataFlowRunResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Triggers a run of a data flow (river) on apply — the Terraform-native way to " +
			"execute the underlying API's activate_river + run actions. This is an imperative action " +
			"modelled as a resource (Terraform provider Actions require Terraform >= 1.14): creating it " +
			"fires one run; change `triggers` (or replace the resource) to fire another. It does not " +
			"track run status or reconcile anything on refresh, and destroying it does not cancel or " +
			"undo a run.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Resource id. Equals the triggered run_id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment the data flow belongs to. Falls back to the provider-level " +
					"environment_id. Changing it forces a new run.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"data_flow_id": schema.StringAttribute{
				Required:      true,
				Description:   "cross_id of the data flow (river) to run. Changing it forces a new run.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"activate": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				Description: "Whether to activate (enable) the data flow before running it. Defaults to " +
					"true. Rivers are created disabled, so the first run needs activation. Changing it " +
					"forces a new run.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"triggers": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Arbitrary key/value pairs that, when changed, force a new run — the same " +
					"pattern as null_resource.triggers. Use e.g. { ts = timestamp() } to run on every " +
					"apply, or a config hash to run when upstream config changes.",
				PlanModifiers: []planmodifier.Map{mapplanmodifier.RequiresReplace()},
			},
			"run_id": schema.StringAttribute{
				Computed:      true,
				Description:   "The id of the run that was triggered.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"run_group_id": schema.StringAttribute{
				Computed:      true,
				Description:   "The run group id returned for the triggered run.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *dataFlowRunResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *dataFlowRunResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dataFlowRunModel
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
	riverID := plan.DataFlowID.ValueString()

	// Activate first (rivers are created disabled). The API may complete
	// synchronously (empty operation id) or defer via an async operation.
	if plan.Activate.ValueBool() {
		opID, err := r.data.client.ActivateDataFlow(ctx, envID, riverID)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Error activating data flow", err)
			return
		}
		if opID != "" && !r.waitForOperation(ctx, envID, opID, &resp.Diagnostics) {
			return
		}
	}

	runID, runGroupID, err := r.data.client.RunDataFlow(ctx, envID, riverID)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error running data flow", err)
		return
	}

	plan.EnvironmentID = types.StringValue(envID)
	plan.ID = types.StringValue(runID)
	plan.RunID = types.StringValue(runID)
	plan.RunGroupID = types.StringValue(runGroupID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read is a no-op: a run is a point-in-time action, not reconcilable state. The
// stored run_id/run_group_id are historical, so we keep prior state untouched.
func (r *dataFlowRunResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dataFlowRunModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update should be unreachable — every input is RequiresReplace, so a changed
// input recreates (= a new run) rather than updating. Implemented for interface
// completeness: carry the plan through.
func (r *dataFlowRunResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dataFlowRunModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is a no-op: a run cannot be un-triggered. Removing the resource just
// drops it from state.
func (r *dataFlowRunResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// waitForOperation polls an async activation operation until it is done ("D").
// Returns false (and appends a diagnostic) on error or timeout.
func (r *dataFlowRunResource) waitForOperation(ctx context.Context, envID, opID string, diags *diag.Diagnostics) bool {
	deadline := time.Now().Add(activationTimeout)
	for {
		status, errMsg, err := r.data.client.GetOperation(ctx, envID, opID)
		if err != nil {
			addAPIError(diags, "Error polling activation operation", err)
			return false
		}
		switch status {
		case "D":
			return true
		case "E":
			diags.AddError("Data flow activation failed",
				fmt.Sprintf("activate_river operation %s reported an error: %s", opID, errMsg))
			return false
		}
		if time.Now().After(deadline) {
			diags.AddError("Data flow activation timed out",
				fmt.Sprintf("activate_river operation %s did not finish within %s (last status %q).",
					opID, activationTimeout, status))
			return false
		}
		select {
		case <-ctx.Done():
			diags.AddError("Data flow activation cancelled", ctx.Err().Error())
			return false
		case <-time.After(2 * time.Second):
		}
	}
}
