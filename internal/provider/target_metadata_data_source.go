package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*targetMetadataDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*targetMetadataDataSource)(nil)
)

// targetTasks maps a target warehouse type to the pull-request task verb that
// lists its top-level containers. The pull_request_type discriminator is always
// "<task>:<target_type>".
var targetTasks = map[string]string{
	"snowflake":  "get_databases",
	"bigquery":   "get_datasets",
	"databricks": "get_catalogs",
}

// NewTargetMetadataDataSource is the factory registered with the provider.
func NewTargetMetadataDataSource() datasource.DataSource { return &targetMetadataDataSource{} }

type targetMetadataDataSource struct {
	data *providerData
}

// ---- Terraform state model -------------------------------------------------

type targetMetadataModel struct {
	ID            types.String     `tfsdk:"id"`
	EnvironmentID types.String     `tfsdk:"environment_id"`
	ConnectionID  types.String     `tfsdk:"connection_id"`
	TargetType    types.String     `tfsdk:"target_type"`
	Timeouts      *smTimeoutsModel `tfsdk:"timeouts"`

	Names      types.List   `tfsdk:"names"`
	ResultJSON types.String `tfsdk:"result_json"`
}

func (d *targetMetadataDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_target_metadata"
}

func (d *targetMetadataDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Discovers the top-level containers of a TARGET data-warehouse connection — " +
			"Snowflake databases, BigQuery datasets, or Databricks catalogs — so a data flow's " +
			"target `database_name`/`dataset` can be chosen from the live warehouse rather than " +
			"hand-typed. It drives the same asynchronous \"pull request\" the console's target picker " +
			"uses: the platform opens a connection to the warehouse and lists its containers. The " +
			"result is exposed both as `names` (a `list(string)` — the common flat-array case, e.g. Snowflake " +
			"`get_databases`) and as `result_json` (the raw operation result as a passthrough string), " +
			"so warehouses that return an array of objects are still usable via `jsondecode()`.",
		Blocks: map[string]schema.Block{
			"timeouts": schema.SingleNestedBlock{
				Description: "How long to wait for the target discovery to finish.",
				Attributes: map[string]schema.Attribute{
					"read": schema.StringAttribute{
						Optional: true,
						Description: "Go duration string (e.g. \"3m\", \"90s\") bounding the discovery " +
							"poll. Default \"3m\".",
					},
				},
			},
		},
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Synthetic id: \"<connection_id>:<task>\" (task is get_databases/get_datasets/get_catalogs).",
			},
			"environment_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Environment ID. Falls back to the provider default.",
			},
			"connection_id": schema.StringAttribute{
				Required:    true,
				Description: "The target warehouse connection (cross_id) to introspect.",
			},
			"target_type": schema.StringAttribute{
				Required: true,
				Description: "The target warehouse type. One of \"snowflake\" (lists databases), " +
					"\"bigquery\" (lists datasets), or \"databricks\" (lists catalogs). Sent as the " +
					"pull-request datasource_id and mapped to the matching task verb.",
			},
			"names": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "The discovered container names (databases/datasets/catalogs) when the " +
					"warehouse returns a flat array of strings. Empty when the warehouse returns a " +
					"richer shape — use `result_json` in that case.",
			},
			"result_json": schema.StringAttribute{
				Computed: true,
				Description: "The raw operation result payload as a JSON string, passed through " +
					"unmodified. For Snowflake this is a JSON array of database-name strings; for " +
					"BigQuery/Databricks it may be an array of objects. Decode with `jsondecode()`.",
			},
		},
	}
}

func (d *targetMetadataDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *targetMetadataDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config targetMetadataModel
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

	targetType := config.TargetType.ValueString()
	task, ok := targetTasks[targetType]
	if !ok {
		resp.Diagnostics.AddError("Invalid target_type",
			fmt.Sprintf("%q is not supported. Use one of: snowflake, bigquery, databricks.", targetType))
		return
	}

	timeout := 3 * time.Minute
	if config.Timeouts != nil && !config.Timeouts.Read.IsNull() && config.Timeouts.Read.ValueString() != "" {
		parsed, err := time.ParseDuration(config.Timeouts.Read.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Invalid timeouts.read",
				fmt.Sprintf("%q is not a valid Go duration (e.g. \"3m\", \"90s\"): %s", config.Timeouts.Read.ValueString(), err))
			return
		}
		timeout = parsed
	}

	body := map[string]any{
		"task_type":     "target",
		"datasource_id": targetType,
		"task":          task,
		"pull_request_inputs": map[string]any{
			"pull_request_type": task + ":" + targetType,
			"connection_id":     config.ConnectionID.ValueString(),
		},
	}
	result, err := d.data.client.DiscoverTargetMetadata(ctx, envID, body, 4*time.Second, timeout)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error discovering target metadata", err)
		return
	}
	if result.Status != "D" {
		resp.Diagnostics.AddError("Target metadata discovery failed",
			fmt.Sprintf("%s operation %s ended with status %q: %s",
				task, result.OperationID, result.Status, result.ErrorMessage))
		return
	}

	raw, err := json.Marshal(result.Result)
	if err != nil {
		resp.Diagnostics.AddError("Error encoding result_json", err.Error())
		return
	}

	names := extractNameStrings(result.Result)
	namesList, diags := types.ListValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := config
	state.EnvironmentID = types.StringValue(envID)
	state.ID = types.StringValue(fmt.Sprintf("%s:%s", config.ConnectionID.ValueString(), task))
	state.Names = namesList
	state.ResultJSON = types.StringValue(string(raw))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// extractNameStrings pulls a flat list of names out of the target result. It
// handles the common Snowflake shape (a JSON array of strings) and, defensively,
// an array of objects keyed by name/database_name/dataset/catalog. Anything it
// cannot flatten yields an empty list (the caller still exposes result_json).
func extractNameStrings(result any) []string {
	arr, ok := result.([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if s := firstString(v, "name", "database_name", "dataset", "catalog", "catalog_name", "id"); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
