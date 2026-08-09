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
const defaultBoomiPlatformURL = "https://api.boomi.com"

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

	// Boomi Platform JWT auth — an alternative to Token. See Configure: this
	// resolves the Data Integration API *identity*, not the account_id used
	// in the API's account-scoped URLs, which is unaffected either way.
	BoomiPlatformURL types.String `tfsdk:"boomi_platform_url"`
	BoomiAccountID   types.String `tfsdk:"boomi_account_id"`
	BoomiUsername    types.String `tfsdk:"boomi_username"`
	BoomiAPIToken    types.String `tfsdk:"boomi_api_token"`
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
			"Authenticates with either a Data Integration API token, or Boomi Platform " +
			"credentials exchanged for a JWT, scoped to an account.",
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
					"DATA_INTEGRATION_API_TOKEN environment variable. Mutually exclusive with the " +
					"boomi_* attributes below — set exactly one auth mode.",
			},
			"account_id": schema.StringAttribute{
				Optional: true,
				Description: "Data Integration account ID. May also be set via the " +
					"DATA_INTEGRATION_ACCOUNT_ID environment variable. Required in every auth " +
					"mode — the API's account-scoped URLs need it regardless of how identity " +
					"is authenticated.",
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Description: "Default environment ID for environment-scoped resources that do " +
					"not set their own environment_id. May also be set via the " +
					"DATA_INTEGRATION_ENVIRONMENT_ID environment variable.",
			},
			"boomi_platform_url": schema.StringAttribute{
				Optional: true,
				Description: "Boomi Platform base URL used to exchange Boomi credentials for a " +
					"JWT. May also be set via the DATA_INTEGRATION_BOOMI_PLATFORM_URL environment " +
					"variable. Defaults to " + defaultBoomiPlatformURL + ".",
			},
			"boomi_account_id": schema.StringAttribute{
				Optional: true,
				Description: "Boomi Platform account ID used in the JWT exchange URL. This is " +
					"distinct from account_id above (the Data Integration account) — the JWT " +
					"resolves identity, not the API's account-scoped URLs. May also be set via " +
					"the DATA_INTEGRATION_BOOMI_ACCOUNT_ID environment variable.",
			},
			"boomi_username": schema.StringAttribute{
				Optional: true,
				Description: "Boomi Platform username. May also be set via the " +
					"DATA_INTEGRATION_BOOMI_USERNAME environment variable.",
			},
			"boomi_api_token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Boomi Platform long-lived API token, exchanged for a short-lived " +
					"JWT that authenticates every request. May also be set via the " +
					"DATA_INTEGRATION_BOOMI_API_TOKEN environment variable.",
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

	// Config value wins, else environment variable, else (for api_url /
	// boomi_platform_url) default.
	apiURL := firstNonEmpty(cfg.APIURL.ValueString(), os.Getenv("DATA_INTEGRATION_API_URL"), defaultAPIURL)
	token := firstNonEmpty(cfg.Token.ValueString(), os.Getenv("DATA_INTEGRATION_API_TOKEN"))
	accountID := firstNonEmpty(cfg.AccountID.ValueString(), os.Getenv("DATA_INTEGRATION_ACCOUNT_ID"))
	defaultEnv := firstNonEmpty(cfg.EnvironmentID.ValueString(), os.Getenv("DATA_INTEGRATION_ENVIRONMENT_ID"))

	boomiPlatformURL := firstNonEmpty(cfg.BoomiPlatformURL.ValueString(),
		os.Getenv("DATA_INTEGRATION_BOOMI_PLATFORM_URL"), defaultBoomiPlatformURL)
	boomiAccountID := firstNonEmpty(cfg.BoomiAccountID.ValueString(), os.Getenv("DATA_INTEGRATION_BOOMI_ACCOUNT_ID"))
	boomiUsername := firstNonEmpty(cfg.BoomiUsername.ValueString(), os.Getenv("DATA_INTEGRATION_BOOMI_USERNAME"))
	boomiAPIToken := firstNonEmpty(cfg.BoomiAPIToken.ValueString(), os.Getenv("DATA_INTEGRATION_BOOMI_API_TOKEN"))
	// Any one Boomi field being set signals intent to use this auth mode, so
	// a partially-filled set (e.g. username but no api_token) is reported as
	// an incomplete Boomi configuration rather than silently falling back.
	boomiRequested := boomiAccountID != "" || boomiUsername != "" || boomiAPIToken != ""

	switch {
	case token != "" && boomiRequested:
		resp.Diagnostics.AddError("Conflicting authentication configuration",
			"Set either \"token\" (DATA_INTEGRATION_API_TOKEN) or the Boomi Platform credentials "+
				"(boomi_account_id, boomi_username, boomi_api_token) — not both.")
	case token == "" && !boomiRequested:
		resp.Diagnostics.AddAttributeError(path.Root("token"),
			"Missing authentication",
			"Set provider attribute \"token\" / DATA_INTEGRATION_API_TOKEN, or supply Boomi "+
				"Platform credentials via boomi_account_id, boomi_username, and boomi_api_token.")
	case boomiRequested:
		if boomiAccountID == "" {
			resp.Diagnostics.AddAttributeError(path.Root("boomi_account_id"),
				"Missing Boomi account ID",
				"Set provider attribute \"boomi_account_id\" or the DATA_INTEGRATION_BOOMI_ACCOUNT_ID environment variable.")
		}
		if boomiUsername == "" {
			resp.Diagnostics.AddAttributeError(path.Root("boomi_username"),
				"Missing Boomi username",
				"Set provider attribute \"boomi_username\" or the DATA_INTEGRATION_BOOMI_USERNAME environment variable.")
		}
		if boomiAPIToken == "" {
			resp.Diagnostics.AddAttributeError(path.Root("boomi_api_token"),
				"Missing Boomi API token",
				"Set provider attribute \"boomi_api_token\" or the DATA_INTEGRATION_BOOMI_API_TOKEN environment variable.")
		}
	}
	if accountID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("account_id"),
			"Missing account ID",
			"Set provider attribute \"account_id\" or the DATA_INTEGRATION_ACCOUNT_ID environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	var tokenSource client.TokenSource
	if boomiRequested {
		var err error
		tokenSource, err = client.NewBoomiJWTSource(client.BoomiJWTSourceConfig{
			PlatformURL: boomiPlatformURL,
			AccountID:   boomiAccountID,
			Username:    boomiUsername,
			APIToken:    boomiAPIToken,
		})
		if err != nil {
			resp.Diagnostics.AddError("Unable to configure Boomi Platform JWT auth", err.Error())
			return
		}
	}

	c, err := client.New(client.Config{
		BaseURL:     apiURL,
		Token:       token,
		TokenSource: tokenSource,
		AccountID:   accountID,
	})
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
		NewLogicodeFileResource,
		NewVariableResource,
		NewDataFlowVariablesResource,
		NewCDCConfigResource,
		NewBlueprintFileResource,
		NewBlueprintResource,
	}
}

func (p *dataIntegrationProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewConnectionTypesDataSource,
		NewConnectionTypeDataSource,
		NewConnectionTestDataSource,
		NewSourceMetadataDataSource,
		NewTargetMetadataDataSource,
		NewSourceTypesDataSource,
		NewTargetTypesDataSource,
		NewDataFlowGroupDataSource,
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
