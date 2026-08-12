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
	_ datasource.DataSource              = (*connectionTestDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*connectionTestDataSource)(nil)
)

// NewConnectionTestDataSource is the factory registered with the provider.
func NewConnectionTestDataSource() datasource.DataSource { return &connectionTestDataSource{} }

type connectionTestDataSource struct {
	data *providerData
}

type connectionTestModel struct {
	ID             types.String `tfsdk:"id"`
	EnvironmentID  types.String `tfsdk:"environment_id"`
	ConnectionID   types.String `tfsdk:"connection_id"`
	DatasourceID   types.String `tfsdk:"datasource_id"`
	Task           types.String `tfsdk:"task"`
	TaskType       types.String `tfsdk:"task_type"`
	InputsJSON     types.String `tfsdk:"inputs_json"`
	TimeoutSeconds types.Int64  `tfsdk:"timeout_seconds"`

	OperationID  types.String `tfsdk:"operation_id"`
	RunID        types.String `tfsdk:"run_id"`
	Status       types.String `tfsdk:"status"`
	Success      types.Bool   `tfsdk:"success"`
	ErrorMessage types.String `tfsdk:"error_message"`
}

func (d *connectionTestDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection_test"
}

func (d *connectionTestDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Tests whether an existing connection can actually reach its source/target and " +
			"authenticate, by having the platform open a real connection to it and " +
			"read metadata (a get_db_metadata/get_schemas \"pull request\"). The API has no dedicated " +
			"test-connection route; this reproduces what the console's \"Test Connection\" button does. " +
			"The read does NOT fail when the connection is unreachable — instead `success` is false and " +
			"`error_message` carries the real connector error (e.g. an ORA-* code). Assert on `success` " +
			"in a lifecycle precondition/check block if you want a bad connection to fail the plan.\n\n" +
			"`task_type` defaults to \"source\" — this tests whether the connection can be PULLED FROM, " +
			"not whether it can be pushed to. For a data-warehouse connection used as a data flow's " +
			"TARGET (Snowflake, BigQuery, Databricks), that default is the wrong check: the API rejects " +
			"a warehouse tested as a source with a 400 \"The connection does not match to the provided " +
			"connection_type\" — and because that is a hard error, not `success = false`, a " +
			"postcondition on `self.success` never even runs; `error_message` is left empty and the " +
			"actual failure surfaces as a provider error instead. Use " +
			"`boomi_data_integration_target_metadata` to check a warehouse target instead — it issues " +
			"the correct `task_type = \"target\"` request and doubles as reachability plus a live list " +
			"of its databases/datasets/catalogs. If you do set `task_type = \"target\"` here directly, " +
			"only the warehouse's own listing verb is accepted (e.g. `get_databases` for Snowflake); " +
			"`get_db_metadata` is rejected with a 422 \"did not match any key in the pull-translate " +
			"mapping for this datasource_id\".\n\n" +
			"This data source only tests DATABASE connections. All three of its tasks " +
			"(`get_db_metadata`, `get_schemas`, `get_databases`) are RDBMS verbs, so a SaaS/API " +
			"connector (Jira, Shopify, Salesforce, …) rejects every one of them with the same 422 " +
			"\"did not match any key in the pull-translate mapping for this datasource_id\"; the " +
			"pull_requests route has no mapping for a SaaS datasource_id under any task name. This " +
			"is a current limitation rather than the intended contract — testing a connection is " +
			"meant to behave uniformly across connector types, and the console already tests SaaS " +
			"connections through a different API surface. Expect the gap to close. Until it does " +
			"there is no pre-flight check for a SaaS connector from Terraform " +
			"(`boomi_data_integration_source_metadata` is RDBMS-only as well), so validate such a " +
			"credential with a run.\n\n" +
			"This test is a live network call and it competes for platform workers. A test that " +
			"completes in ~35s on its own can sit at operation status \"R\" past the 180s default when " +
			"Terraform reads it concurrently with other live data sources, which it does by default. A " +
			"timeout here is a hard provider error, not `success = false`, so a postcondition cannot " +
			"catch it — raise `timeout_seconds`, run with `-parallelism=1`, or prefer " +
			"`boomi_data_integration_source_metadata`, which proves the same connection AND returns the " +
			"schema mapping the data flow needs, replacing this test rather than adding to it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:    true,
				Description: "Equals operation_id — the id of the pull-request operation that ran the test.",
			},
			"environment_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Environment ID. Falls back to the provider default.",
			},
			"connection_id": schema.StringAttribute{
				Required:    true,
				Description: "The connection (cross_id) to test.",
			},
			"datasource_id": schema.StringAttribute{
				Required: true,
				Description: "The source/target type slug of the connection (e.g. \"oracle\", \"mysql\", " +
					"\"postgres\", \"snowflake\"). Used to build the typed pull-request.",
			},
			"task": schema.StringAttribute{
				Optional: true,
				Description: "The pull-request operation that opens the connection. Defaults to " +
					"\"get_db_metadata\". Other reachability tasks: \"get_schemas\", \"get_databases\".",
			},
			"task_type": schema.StringAttribute{
				Optional: true,
				Description: "Whether the connection is used as a \"source\" (default) or \"target\". " +
					"Get this wrong for a data-warehouse connection (Snowflake/BigQuery/Databricks) and " +
					"the API returns a hard 400 error rather than `success = false` — see the data " +
					"source description for why `boomi_data_integration_target_metadata` is the right " +
					"tool for a warehouse target instead.",
			},
			"inputs_json": schema.StringAttribute{
				Optional: true,
				Description: "Optional extra pull_request_inputs fields as a JSON object, merged into the " +
					"request (e.g. {\"database_name\":\"MYDB\"} for Snowflake, or {\"schemas\":[\"DEV\"]}). " +
					"connection_id and pull_request_type are always set by the provider.",
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional:    true,
				Description: "How long to wait for the test to finish before erroring. Default 180.",
			},
			"operation_id": schema.StringAttribute{
				Computed:    true,
				Description: "The pull-request operation id.",
			},
			"run_id": schema.StringAttribute{
				Computed:    true,
				Description: "The run id of the test operation (useful for fetching logs).",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Terminal operation status: \"D\" (done/reachable) or \"E\" (error).",
			},
			"success": schema.BoolAttribute{
				Computed:    true,
				Description: "True when the connection was reachable and authenticated (status == \"D\").",
			},
			"error_message": schema.StringAttribute{
				Computed:    true,
				Description: "The connector error when success is false (empty on success).",
			},
		},
	}
}

func (d *connectionTestDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *connectionTestDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config connectionTestModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	task := config.Task.ValueString()
	if task == "" {
		task = "get_db_metadata"
	}
	taskType := config.TaskType.ValueString()
	if taskType == "" {
		taskType = "source"
	}
	timeout := 180 * time.Second
	if !config.TimeoutSeconds.IsNull() && config.TimeoutSeconds.ValueInt64() > 0 {
		timeout = time.Duration(config.TimeoutSeconds.ValueInt64()) * time.Second
	}
	datasourceID := config.DatasourceID.ValueString()

	// pull_request_inputs: always connection_id + pull_request_type ("<task>:<datasource_id>"),
	// plus any user-supplied extra fields.
	inputs := map[string]any{
		"connection_id":     config.ConnectionID.ValueString(),
		"pull_request_type": fmt.Sprintf("%s:%s", task, datasourceID),
	}
	if !config.InputsJSON.IsNull() && config.InputsJSON.ValueString() != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(config.InputsJSON.ValueString()), &extra); err != nil {
			resp.Diagnostics.AddError("Invalid inputs_json", fmt.Sprintf("inputs_json must be a JSON object: %s", err))
			return
		}
		for k, v := range extra {
			inputs[k] = v
		}
	}

	body := map[string]any{
		"task_type":           taskType,
		"datasource_id":       datasourceID,
		"task":                task,
		"pull_request_inputs": inputs,
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

	result, err := d.data.client.TestConnection(ctx, envID, body, 4*time.Second, timeout)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error running connection test", err)
		return
	}

	state := config
	state.EnvironmentID = types.StringValue(envID)
	state.Task = types.StringValue(task)
	state.TaskType = types.StringValue(taskType)
	state.ID = types.StringValue(result.OperationID)
	state.OperationID = types.StringValue(result.OperationID)
	state.RunID = types.StringValue(result.RunID)
	state.Status = types.StringValue(result.Status)
	state.Success = types.BoolValue(result.Status == "D")
	state.ErrorMessage = types.StringValue(result.ErrorMessage)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
