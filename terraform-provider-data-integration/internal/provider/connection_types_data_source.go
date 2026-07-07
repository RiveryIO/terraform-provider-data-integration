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
	_ datasource.DataSource              = (*connectionTypesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*connectionTypesDataSource)(nil)
)

// NewConnectionTypesDataSource is the factory registered with the provider.
func NewConnectionTypesDataSource() datasource.DataSource { return &connectionTypesDataSource{} }

type connectionTypesDataSource struct {
	data *providerData
}

type connectionTypeSummary struct {
	ConnectionType     types.String `tfsdk:"connection_type"`
	ConnectionTypeName types.String `tfsdk:"connection_type_name"`
	IsTestConnection   types.Bool   `tfsdk:"is_test_connection"`
}

type connectionTypesModel struct {
	ID              types.String            `tfsdk:"id"`
	ConnectionTypes []connectionTypeSummary `tfsdk:"connection_types"`
}

func (d *connectionTypesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection_types"
}

func (d *connectionTypesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The catalog of connection types supported by the Data Integration API. Read " +
			"directly from the live API, so new connector types appear without a provider release. Use " +
			"it to discover the `type` value for a boomi_connection, and pair it with the " +
			"boomi_connection_type data source to discover each type's configurable fields.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Static identifier for this catalog data source.",
			},
			"connection_types": schema.ListNestedAttribute{
				Computed:    true,
				Description: "All available connection types, sorted by connection_type.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"connection_type": schema.StringAttribute{
							Computed:    true,
							Description: "The type identifier used as boomi_connection.type (e.g. \"mysql\").",
						},
						"connection_type_name": schema.StringAttribute{
							Computed:    true,
							Description: "Human-readable name (e.g. \"MySQL\").",
						},
						"is_test_connection": schema.BoolAttribute{
							Computed:    true,
							Description: "Whether this connection type supports a test-connection action.",
						},
					},
				},
			},
		},
	}
}

func (d *connectionTypesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *connectionTypesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	items, err := d.data.client.ListConnectionTypes(ctx)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error listing connection types", err)
		return
	}

	var state connectionTypesModel
	for _, it := range items {
		state.ConnectionTypes = append(state.ConnectionTypes, connectionTypeSummary{
			ConnectionType:     types.StringValue(asString(it["connection_type"])),
			ConnectionTypeName: types.StringValue(asString(it["connection_type_name"])),
			IsTestConnection:   types.BoolValue(isTrue(it["is_test_connection"])),
		})
	}
	// Stable ordering so the data source doesn't churn between reads.
	sort.Slice(state.ConnectionTypes, func(i, j int) bool {
		return state.ConnectionTypes[i].ConnectionType.ValueString() < state.ConnectionTypes[j].ConnectionType.ValueString()
	})
	state.ID = types.StringValue("connection_types")
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
