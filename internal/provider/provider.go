// Package provider implements the Boomi Data Integration Terraform provider.
//
// The public surface uses Data Integration terminology — the provider type
// name (resource prefix) is "boomi_data_integration", so resources are
// boomi_data_integration_data_flow / boomi_data_integration_connection /
// boomi_data_integration_environment. The provider's HCL local name stays
// "boomi" (Terraform forbids underscores in provider local names, and infers
// the provider from the first segment of the resource type) — even though the
// underlying API speaks "river".
package provider

import (
	"context"
	"os"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const defaultAPIURL = "https://api.rivery.io"

// Ensure the provider satisfies the framework interface.
var _ provider.Provider = (*dataIntegrationProvider)(nil)

// dataIntegrationProvider is the provider implementation.
type dataIntegrationProvider struct {
	// version is set at build time and surfaced in the user agent.
	version string
}

// providerModel maps provider schema attributes to Go types.
type providerModel struct {
	APIURL        types.String `tfsdk:"api_url"`
	Token         types.String `tfsdk:"token"`
	AccountID     types.String `tfsdk:"account_id"`
	EnvironmentID types.String `tfsdk:"environment_id"`
}

// New returns a provider factory for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &dataIntegrationProvider{version: version}
	}
}

func (p *dataIntegrationProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	// TypeName is the resource/data-source prefix: boomi_data_integration_*.
	// It differs from the provider local name (`boomi`) and the registry type
	// (`data-integration`) on purpose — see "Provider naming" in README.md.
	// Docs are generated with `--provider-name boomi`; changing this prefix
	// requires updating that pipeline and regenerating docs/.
	resp.TypeName = "boomi_data_integration"
	resp.Version = p.version
}

func (p *dataIntegrationProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage Boomi Data Integration resources as code. " +
			"Authenticates with a Data Integration API token scoped to an account.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				Optional: true,
				Description: "Base URL of the Data Integration API. May also be set via the " +
					"DATA_INTEGRATION_API_URL environment variable. Defaults to " + defaultAPIURL + ".",
			},
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Data Integration API token (bearer). May also be set via the " +
					"DATA_INTEGRATION_API_TOKEN environment variable.",
			},
			"account_id": schema.StringAttribute{
				Optional: true,
				Description: "Data Integration account ID. May also be set via the " +
					"DATA_INTEGRATION_ACCOUNT_ID environment variable.",
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Description: "Default environment ID for environment-scoped resources that do " +
					"not set their own environment_id. May also be set via the " +
					"DATA_INTEGRATION_ENVIRONMENT_ID environment variable.",
			},
		},
	}
}

// providerData is handed to every resource via Configure. It carries the
// API client and the provider-level default environment id.
type providerData struct {
	client               *client.Client
	defaultEnvironmentID string
}

func (p *dataIntegrationProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Config value wins, else environment variable, else (for api_url) default.
	apiURL := firstNonEmpty(cfg.APIURL.ValueString(), os.Getenv("DATA_INTEGRATION_API_URL"), defaultAPIURL)
	token := firstNonEmpty(cfg.Token.ValueString(), os.Getenv("DATA_INTEGRATION_API_TOKEN"))
	accountID := firstNonEmpty(cfg.AccountID.ValueString(), os.Getenv("DATA_INTEGRATION_ACCOUNT_ID"))
	defaultEnv := firstNonEmpty(cfg.EnvironmentID.ValueString(), os.Getenv("DATA_INTEGRATION_ENVIRONMENT_ID"))

	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("token"),
			"Missing API token",
			"Set provider attribute \"token\" or the DATA_INTEGRATION_API_TOKEN environment variable.")
	}
	if accountID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("account_id"),
			"Missing account ID",
			"Set provider attribute \"account_id\" or the DATA_INTEGRATION_ACCOUNT_ID environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(client.Config{BaseURL: apiURL, Token: token, AccountID: accountID})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Data Integration API client", err.Error())
		return
	}

	data := &providerData{client: c, defaultEnvironmentID: defaultEnv}
	resp.DataSourceData = data
	resp.ResourceData = data
}

func (p *dataIntegrationProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewEnvironmentResource,
		NewConnectionResource,
		NewDataFlowResource,
		NewDataFlowRunResource,
		NewDataFrameResource,
		NewVariableResource,
		NewCDCConfigResource,
	}
}

func (p *dataIntegrationProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewConnectionTypesDataSource,
		NewConnectionTypeDataSource,
		NewSourceTypesDataSource,
		NewTargetTypesDataSource,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
