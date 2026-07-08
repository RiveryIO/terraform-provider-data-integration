package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*targetTypesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*targetTypesDataSource)(nil)
)

// NewTargetTypesDataSource is the factory registered with the provider.
func NewTargetTypesDataSource() datasource.DataSource { return &targetTypesDataSource{} }

type targetTypesDataSource struct {
	data *providerData
}

type targetTypeSummary struct {
	TargetType     types.String `tfsdk:"target_type"`
	Name           types.String `tfsdk:"name"`
	ConnectionType types.String `tfsdk:"connection_type"`
	LogicStepType  types.String `tfsdk:"logic_step_type"`
	DataFlowTypeID types.String `tfsdk:"data_flow_type_id"`
}

type targetTypesModel struct {
	ID          types.String        `tfsdk:"id"`
	TargetTypes []targetTypeSummary `tfsdk:"target_types"`
}

func (d *targetTypesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_target_types"
}

func (d *targetTypesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The catalog of target types supported by the Data Integration API, read from the " +
			"live API. Use it to discover the `target_type` that goes in a source-to-target data " +
			"flow's properties.target.name and the connection_type each target binds to. New targets " +
			"appear without a provider release.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Static identifier for this catalog data source.",
			},
			"target_types": schema.ListNestedAttribute{
				Computed:    true,
				Description: "All available target types, sorted by target_type.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"target_type": schema.StringAttribute{
							Computed:    true,
							Description: "Target type identifier (e.g. \"snowflake\", \"bq\").",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Human-readable name (e.g. \"Google BigQuery (Target)\").",
						},
						"connection_type": schema.StringAttribute{
							Computed:    true,
							Description: "The connection type this target binds to (use with boomi_connection.type).",
						},
						"logic_step_type": schema.StringAttribute{
							Computed:    true,
							Description: "The logic-step type id associated with this target.",
						},
						"data_flow_type_id": schema.StringAttribute{
							Computed:    true,
							Description: "The data-flow type this target applies to (e.g. src_to_trgt).",
						},
					},
				},
			},
		},
	}
}

func (d *targetTypesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type",
			fmt.Sprintf("Expected *providerData, got %T. This is a provider bug.", req.ProviderData))
		return
	}
	d.data = data
}

func (d *targetTypesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	items, err := d.data.client.ListTargetTypes(ctx)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error listing target types", err)
		return
	}

	var state targetTypesModel
	for _, it := range items {
		state.TargetTypes = append(state.TargetTypes, targetTypeSummary{
			TargetType:     types.StringValue(asString(it["target_type"])),
			Name:           types.StringValue(asString(it["name"])),
			ConnectionType: types.StringValue(asString(it["connection_type"])),
			LogicStepType:  types.StringValue(asString(it["logic_step_type"])),
			DataFlowTypeID: types.StringValue(asString(it["river_type_id"])),
		})
	}
	sort.Slice(state.TargetTypes, func(i, j int) bool {
		return state.TargetTypes[i].TargetType.ValueString() < state.TargetTypes[j].TargetType.ValueString()
	})
	state.ID = types.StringValue("target_types")
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
