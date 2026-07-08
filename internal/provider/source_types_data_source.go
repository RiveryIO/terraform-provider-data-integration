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
	_ datasource.DataSource              = (*sourceTypesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*sourceTypesDataSource)(nil)
)

// NewSourceTypesDataSource is the factory registered with the provider.
func NewSourceTypesDataSource() datasource.DataSource { return &sourceTypesDataSource{} }

type sourceTypesDataSource struct {
	data *providerData
}

type sourceTypeSummary struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	ConnectionType   types.String `tfsdk:"connection_type"`
	Status           types.String `tfsdk:"status"`
	SectionID        types.String `tfsdk:"section_id"`
	DocumentationURL types.String `tfsdk:"documentation_url"`
}

type sourceTypesModel struct {
	ID          types.String        `tfsdk:"id"`
	SourceTypes []sourceTypeSummary `tfsdk:"source_types"`
}

func (d *sourceTypesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_source_types"
}

func (d *sourceTypesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The catalog of source (datasource) types supported by the Data Integration API, " +
			"read from the live API. Use it to discover the `id` that goes in a source-to-target " +
			"data flow's properties.source.name, and the connection_type each source binds to. New " +
			"sources appear without a provider release.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Static identifier for this catalog data source.",
			},
			"source_types": schema.ListNestedAttribute{
				Computed:    true,
				Description: "All available source types, sorted by id.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:    true,
							Description: "Source type identifier (e.g. \"mysql\", \"salesforce\").",
						},
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Human-readable name (e.g. \"MySQL\").",
						},
						"connection_type": schema.StringAttribute{
							Computed:    true,
							Description: "The connection type this source binds to (use with boomi_connection.type).",
						},
						"status": schema.StringAttribute{
							Computed:    true,
							Description: "Availability status (e.g. enabled, coming_soon, sunset).",
						},
						"section_id": schema.StringAttribute{
							Computed:    true,
							Description: "Catalog section/category id.",
						},
						"documentation_url": schema.StringAttribute{
							Computed:    true,
							Description: "Link to the source's documentation.",
						},
					},
				},
			},
		},
	}
}

func (d *sourceTypesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *sourceTypesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	items, err := d.data.client.ListSourceTypes(ctx)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error listing source types", err)
		return
	}

	var state sourceTypesModel
	for _, it := range items {
		state.SourceTypes = append(state.SourceTypes, sourceTypeSummary{
			ID:               types.StringValue(asString(it["id"])),
			Name:             types.StringValue(asString(it["name"])),
			ConnectionType:   types.StringValue(asString(it["connection_type"])),
			Status:           types.StringValue(asString(it["status"])),
			SectionID:        types.StringValue(asString(it["section_id"])),
			DocumentationURL: types.StringValue(asString(it["documentation_url"])),
		})
	}
	sort.Slice(state.SourceTypes, func(i, j int) bool {
		return state.SourceTypes[i].ID.ValueString() < state.SourceTypes[j].ID.ValueString()
	})
	state.ID = types.StringValue("source_types")
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
