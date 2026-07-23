package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*dataFlowVariablesResource)(nil)
	_ resource.ResourceWithConfigure   = (*dataFlowVariablesResource)(nil)
	_ resource.ResourceWithImportState = (*dataFlowVariablesResource)(nil)
)

func NewDataFlowVariablesResource() resource.Resource { return &dataFlowVariablesResource{} }

type dataFlowVariablesResource struct {
	data *providerData
}

// dataFlowVariableModel is the Terraform schema model for one variable block.
type dataFlowVariableModel struct {
	Name              types.String `tfsdk:"name"`
	Value             types.String `tfsdk:"value"`
	IsMultiValue      types.Bool   `tfsdk:"is_multi_value"`
	IsEncrypted       types.Bool   `tfsdk:"is_encrypted"`
	ClearValueOnStart types.Bool   `tfsdk:"clear_value_on_start"`
}

// dataFlowVariablesModel is the top-level resource model.
type dataFlowVariablesModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	DataFlowID       types.String `tfsdk:"data_flow_id"`
	Variables     types.List   `tfsdk:"variable"`
}

var dataFlowVariableAttrTypes = map[string]attr.Type{
	"name":                 types.StringType,
	"value":                types.StringType,
	"is_multi_value":       types.BoolType,
	"is_encrypted":         types.BoolType,
	"clear_value_on_start": types.BoolType,
}

func (r *dataFlowVariablesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_data_flow_variables"
}

func (r *dataFlowVariablesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the full set of variables for a single data flow. Uses replace-all " +
			"semantics: variables not listed in this resource are deleted. Encrypted variable " +
			"values are written as plaintext and stored as the stable API-returned ciphertext " +
			"in state; drift detection works because the ciphertext only changes when a new " +
			"plaintext is written. Multi-value variable values are stored as a JSON array string.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Resource ID. Equals <environment_id>/<data_flow_id>.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Environment the data flow belongs to. Falls back to the provider-level environment_id.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"data_flow_id": schema.StringAttribute{
				Required:      true,
				Description:   "Cross-ID of the data flow whose variables this resource manages. Changing it forces a new resource.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
		},
		Blocks: map[string]schema.Block{
			"variable": schema.ListNestedBlock{
				Description: "A data flow variable. Order is preserved as returned by the API.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:    true,
							Description: "Variable name.",
						},
						"value": schema.StringAttribute{
							Required:  true,
							Sensitive: true,
							Description: "Variable value. For multi-value variables, provide a JSON array string (e.g. '[1,2]'). " +
								"For encrypted variables, provide the plaintext — the API encrypts it and stores the ciphertext.",
						},
						"is_multi_value": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(false),
							Description: "Whether this variable holds multiple values (array).",
						},
						"is_encrypted": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(false),
							Description: "Whether the value is encrypted at rest. Provide plaintext on write; state stores ciphertext.",
						},
						"clear_value_on_start": schema.BoolAttribute{
							Optional:    true,
							Computed:    true,
							Default:     booldefault.StaticBool(false),
							Description: "Whether the runtime clears this variable's value at the start of each run.",
						},
					},
				},
			},
		},
	}
}

func (r *dataFlowVariablesResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

// valueToString serialises the API's any-typed value to the string stored in state.
func valueToString(v client.RiverVariable) (string, error) {
	if v.Settings.IsMultiValue {
		b, err := json.Marshal(v.Value)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	if s, ok := v.Value.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(v.Value)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// jsonNormalize round-trips a JSON string through unmarshal/marshal to produce
// compact canonical form. Returns the original string on parse error.
func jsonNormalize(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}

// modelToAPI converts the TF variable blocks to the API request body items.
// The value string is decoded appropriately for each variable type.
func modelToAPI(vars []dataFlowVariableModel) ([]client.RiverVariable, error) {
	items := make([]client.RiverVariable, 0, len(vars))
	for _, v := range vars {
		var apiVal any
		if v.IsMultiValue.ValueBool() {
			var arr []any
			if err := json.Unmarshal([]byte(v.Value.ValueString()), &arr); err != nil {
				return nil, errors.New("variable " + v.Name.ValueString() + ": is_multi_value=true but value is not a valid JSON array: " + err.Error())
			}
			apiVal = arr
		} else {
			apiVal = v.Value.ValueString()
		}
		items = append(items, client.RiverVariable{
			Name: v.Name.ValueString(),
			Settings: client.DataFlowVariableSettings{
				ClearValueOnStart: v.ClearValueOnStart.ValueBool(),
				IsMultiValue:      v.IsMultiValue.ValueBool(),
				IsEncrypted:       v.IsEncrypted.ValueBool(),
			},
			Value: apiVal,
		})
	}
	return items, nil
}

func (r *dataFlowVariablesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dataFlowVariablesModel
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

	var varBlocks []dataFlowVariableModel
	resp.Diagnostics.Append(plan.Variables.ElementsAs(ctx, &varBlocks, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	items, err := modelToAPI(varBlocks)
	if err != nil {
		resp.Diagnostics.AddError("Invalid variable value", err.Error())
		return
	}

	dataFlowID := plan.DataFlowID.ValueString()
	result, err := r.data.client.PutDataFlowVariables(ctx, envID, dataFlowID, items)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating data flow variables", err)
		return
	}

	// Write plan values directly to state — avoids JSON normalisation differences
	// (e.g. "[1, 2]" vs "[1,2]") and the plaintext/ciphertext mismatch for encrypted vars.
	_ = result // confirmed the PUT succeeded; state is sourced from the plan

	plan.ID = types.StringValue(envID + "/" + dataFlowID)
	plan.EnvironmentID = types.StringValue(envID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dataFlowVariablesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dataFlowVariablesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	items, err := r.data.client.ListDataFlowVariables(ctx, state.EnvironmentID.ValueString(), state.DataFlowID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading data flow variables", err)
		return
	}

	// For encrypted variables, keep the state value unchanged if the variable still
	// exists — the ciphertext is stable so if it matches state we know nothing changed;
	// if the user changed their config plaintext we see a diff on the value attribute.
	// Build a name→stateValue map for the lookup.
	stateVars := map[string]string{}
	var stateBlocks []dataFlowVariableModel
	resp.Diagnostics.Append(state.Variables.ElementsAs(ctx, &stateBlocks, false)...)
	if !resp.Diagnostics.HasError() {
		for _, v := range stateBlocks {
			stateVars[v.Name.ValueString()] = v.Value.ValueString()
		}
	}

	// Build the final list, choosing the value string per variable type:
	// - Encrypted: keep state value (no decrypt API; plaintext in state, ciphertext from API).
	// - Multi-value: keep state value when JSON-normalised forms are semantically equal,
	//   to avoid spurious diffs from whitespace ("[1, 2]" vs "[1,2]"). Use API value on
	//   an out-of-band change.
	// - Single: use API value directly (plain strings are stable).
	// We build the list directly here rather than going through apiToModel so that we can
	// pass the value string directly — assigning a string to v.Value and then letting
	// valueToString json.Marshal it would double-encode the string for multi-value vars.
	elems := make([]attr.Value, 0, len(items))
	for _, v := range items {
		var valStr string
		sv, inState := stateVars[v.Name]
		if v.Settings.IsEncrypted && inState {
			valStr = sv
		} else if v.Settings.IsMultiValue && inState {
			apiStr, err := valueToString(v)
			if err == nil && jsonNormalize(apiStr) == jsonNormalize(sv) {
				valStr = sv // semantically equal — preserve config formatting
			} else {
				valStr = apiStr // out-of-band change, use API value
			}
		} else {
			var err error
			valStr, err = valueToString(v)
			if err != nil {
				resp.Diagnostics.AddError("Error serialising variable value", err.Error())
				return
			}
		}
		obj, diags := types.ObjectValue(dataFlowVariableAttrTypes, map[string]attr.Value{
			"name":                 types.StringValue(v.Name),
			"value":                types.StringValue(valStr),
			"is_multi_value":       types.BoolValue(v.Settings.IsMultiValue),
			"is_encrypted":         types.BoolValue(v.Settings.IsEncrypted),
			"clear_value_on_start": types.BoolValue(v.Settings.ClearValueOnStart),
		})
		if diags.HasError() {
			resp.Diagnostics.AddError("Error building variable object", diags.Errors()[0].Detail())
			return
		}
		elems = append(elems, obj)
	}
	listVal, diags := types.ListValue(types.ObjectType{AttrTypes: dataFlowVariableAttrTypes}, elems)
	if diags.HasError() {
		resp.Diagnostics.AddError("Error building variable list", diags.Errors()[0].Detail())
		return
	}

	state.Variables = listVal
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *dataFlowVariablesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dataFlowVariablesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var varBlocks []dataFlowVariableModel
	resp.Diagnostics.Append(plan.Variables.ElementsAs(ctx, &varBlocks, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	items, err := modelToAPI(varBlocks)
	if err != nil {
		resp.Diagnostics.AddError("Invalid variable value", err.Error())
		return
	}

	result, err := r.data.client.PutDataFlowVariables(ctx, plan.EnvironmentID.ValueString(), plan.DataFlowID.ValueString(), items)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating data flow variables", err)
		return
	}

	_ = result // confirmed the PUT succeeded; state is sourced from the plan
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dataFlowVariablesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dataFlowVariablesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.data.client.PutDataFlowVariables(ctx, state.EnvironmentID.ValueString(), state.DataFlowID.ValueString(), []client.RiverVariable{}); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			addAPIError(&resp.Diagnostics, "Error deleting data flow variables", err)
		}
	}
}

// ImportState accepts "<environment_id>/<data_flow_id>".
func (r *dataFlowVariablesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Use \"<environment_id>/<data_flow_id>\".")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("data_flow_id"), parts[1])...)
}
