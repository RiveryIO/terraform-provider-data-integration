package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*connectionTypeDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*connectionTypeDataSource)(nil)
)

// NewConnectionTypeDataSource is the factory registered with the provider.
func NewConnectionTypeDataSource() datasource.DataSource { return &connectionTypeDataSource{} }

type connectionTypeDataSource struct {
	data *providerData
}

type connectionTypeModel struct {
	ID                 types.String `tfsdk:"id"`
	ConnectionType     types.String `tfsdk:"connection_type"`
	ConnectionTypeName types.String `tfsdk:"connection_type_name"`
	PropertyNames      types.List   `tfsdk:"property_names"`
	PropertiesJSON     types.String `tfsdk:"properties_json"`
}

func (d *connectionTypeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection_type"
}

func (d *connectionTypeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The property schema of a single connection type, read from the live Data " +
			"Integration API. Use it to discover which fields belong in a boomi_connection's " +
			"parameters_json for a given type — the field set stays current as the API evolves, with " +
			"no provider release required. `properties_json` carries the full, raw property schema " +
			"(each type's shape differs), and `property_names` extracts the field ids for convenience.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Equals connection_type.",
			},
			"connection_type": schema.StringAttribute{
				Required:    true,
				Description: "The connection type to look up (e.g. \"mysql\", \"postgres\", \"snowflake\").",
			},
			"connection_type_name": schema.StringAttribute{
				Computed:    true,
				Description: "Human-readable name (e.g. \"MySQL\").",
			},
			"property_names": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "The ids of the type's configurable properties — the keys valid in a " +
					"boomi_connection parameters_json for this type.",
			},
			"properties_json": schema.StringAttribute{
				Computed: true,
				Description: "The full property schema as raw JSON (a list of property objects, each " +
					"with id/type and optional display_name/ui_type/default_value/etc.). Kept opaque " +
					"because each connection type's property shape differs.",
			},
		},
	}
}

func (d *connectionTypeDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *connectionTypeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config connectionTypeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	connType := config.ConnectionType.ValueString()

	ct, err := d.data.client.GetConnectionType(ctx, connType)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error reading connection type", err)
		return
	}

	state := connectionTypeModel{
		ID:                 types.StringValue(connType),
		ConnectionType:     types.StringValue(connType),
		ConnectionTypeName: types.StringValue(asString(ct["connection_type_name"])),
	}

	// Extract property ids and preserve the full schema as raw JSON.
	var names []string
	if props, ok := ct["properties"].([]any); ok {
		for _, p := range props {
			if pm, ok := p.(map[string]any); ok {
				if id := asString(pm["id"]); id != "" {
					names = append(names, id)
				}
			}
		}
		if raw, err := json.Marshal(props); err == nil {
			state.PropertiesJSON = types.StringValue(string(raw))
		} else {
			resp.Diagnostics.AddError("Error encoding properties", err.Error())
			return
		}
	} else {
		state.PropertiesJSON = types.StringValue("[]")
	}

	nameList, diags := types.ListValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.PropertyNames = nameList

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
