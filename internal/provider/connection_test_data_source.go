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
			"authenticate, by asking the Data Integration worker fleet to open a real connection and " +
			"read metadata (a get_db_metadata/get_schemas \"pull request\"). The API has no dedicated " +
			"test-connection route; this reproduces what the console's \"Test Connection\" button does. " +
			"The read does NOT fail when the connection is unreachable — instead `success` is false and " +
			"`error_message` carries the real connector error (e.g. an ORA-* code). Assert on `success` " +
			"in a lifecycle precondition/check block if you want a bad connection to fail the plan.",
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
				Optional:    true,
				Description: "Whether the connection is used as a \"source\" (default) or \"target\".",
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
