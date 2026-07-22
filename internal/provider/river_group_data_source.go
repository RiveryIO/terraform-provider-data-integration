package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*riverGroupDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*riverGroupDataSource)(nil)
)

// NewRiverGroupDataSource is the factory registered with the provider.
func NewRiverGroupDataSource() datasource.DataSource { return &riverGroupDataSource{} }

type riverGroupDataSource struct{ data *providerData }

type riverGroupDataModel struct {
	ID            types.String `tfsdk:"id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
	Name          types.String `tfsdk:"name"`
	Color         types.String `tfsdk:"color"`
	Icon          types.String `tfsdk:"icon"`
	IsDefault     types.Bool   `tfsdk:"is_default"`
}

func (d *riverGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_river_group"
}

func (d *riverGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up an existing river group (folder) by name. " +
			"Groups must be created in the Data Integration UI — the v1 API exposes read-only access. " +
			"Use this data source to obtain a group's cross_id and pass it as group_id to data flow resources.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Cross ID of the group (use as group_id on data flow resources).",
			},
			"environment_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Environment ID. Falls back to the provider default.",
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Exact name of the group to look up.",
			},
			"color": schema.StringAttribute{
				Computed:    true,
				Description: "Hex colour assigned to the group in the UI.",
			},
			"icon": schema.StringAttribute{
				Computed:    true,
				Description: "Icon slug assigned to the group in the UI.",
			},
			"is_default": schema.BoolAttribute{
				Computed:    true,
				Description: "True if this is the environment's default group.",
			},
		},
	}
}

func (d *riverGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type",
			fmt.Sprintf("Expected *providerData, got %T. This is a provider bug.", req.ProviderData))
		return
	}
	d.data = pd
}

func (d *riverGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config riverGroupDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	envID := config.EnvironmentID.ValueString()
	if envID == "" {
		envID = d.data.defaultEnvironmentID
	}
	if envID == "" {
		resp.Diagnostics.AddError("Missing environment_id",
			"Set environment_id on the data source or configure a provider default.")
		return
	}

	groups, err := d.data.client.ListRiverGroups(ctx, envID)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error listing river groups", err)
		return
	}

	want := config.Name.ValueString()
	for _, g := range groups {
		if g.Name == want {
			resp.Diagnostics.Append(resp.State.Set(ctx, &riverGroupDataModel{
				ID:            types.StringValue(g.ID),
				EnvironmentID: types.StringValue(envID),
				Name:          types.StringValue(g.Name),
				Color:         types.StringValue(g.Color),
				Icon:          types.StringValue(g.Icon),
				IsDefault:     types.BoolValue(g.IsDefault),
			})...)
			return
		}
	}

	resp.Diagnostics.AddError(
		"River group not found",
		fmt.Sprintf("No group named %q exists in environment %s. Create it in the Data Integration UI first.", want, envID),
	)
}
