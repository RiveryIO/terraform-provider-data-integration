package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
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
			"This test is a live network call and it competes for platform workers. A test that " +
			"completes in ~35s on its own can sit at operation status \"R\" past the 180s default when " +
			"Terraform reads it concurrently with other live data sources, which it does by default. A " +
			"timeout here now surfaces as `success = false` (status stays \"R\", `error_message` carries " +
			"the timeout detail) plus a warning diagnostic — not a hard provider error — so a " +
			"postcondition on `self.success` DOES catch it. Mitigations, best first: keep " +
			"`timeout_seconds` SMALL so an inconclusive gate fails fast; stage the apply so gates do " +
			"not front-run the work (apply the connections, then the data flow, then let the gates " +
			"read); and only then consider `-parallelism=1`. Do NOT combine a raised " +
			"`timeout_seconds` with `-parallelism=1`: serialising the reads makes their timeouts SUM, " +
			"and they sum in front of resource creation — two gates at 600s and 5m become a " +
			"15-minute prologue ahead of a data flow that takes ~60s to create. " +
			"`boomi_data_integration_source_metadata` is an alternative that proves the same " +
			"connection AND returns the schema mapping the data flow needs, replacing this test " +
			"rather than adding to it — but note it issues one live request per requested table, " +
			"serially, so for a multi-table flow it is not cheaper.",
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
				Optional: true,
				Description: "How long to wait for the test to finish before giving up. Default 180. On " +
					"expiry the read does NOT error: it returns `success = false` (status stays \"R\") " +
					"plus a warning diagnostic, so a `postcondition` on `self.success` still catches it.",
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

	// A timeout is NOT treated like other errors here. TestConnection still
	// returns the partially-populated result (OperationID, RunID, and the
	// last-seen Status — typically "R") even when it also returns an error, so
	// there is a real, if inconclusive, result to report. Falling through to
	// the normal state-set below means Status != "D" makes Success false on
	// its own, which is exactly what lets the `postcondition { condition =
	// self.success }` pattern this data source's docs recommend actually run
	// — a hard error here would skip the postcondition entirely, which is the
	// bug this branch exists to avoid. A warning is still raised so a caller
	// who did NOT write a postcondition sees that the test was inconclusive,
	// rather than silently reading `success = false` as if the connection
	// itself failed.
	timedOut := errors.Is(err, client.ErrConnectionTestTimeout)
	if err != nil && !timedOut {
		// The most common non-timeout failure is a warehouse connection
		// (Snowflake/BigQuery/Databricks) tested with the source-shaped
		// defaults: task_type defaults to "source", but a warehouse only
		// accepts a "target" pull request, so the API returns a 400 with this
		// exact detail text. targetTasks (defined in
		// target_metadata_data_source.go) tells us both that datasource_id is
		// a warehouse and which task verb it actually accepts, so we can name
		// the fix instead of surfacing a bare 400.
		if targetTask, isWarehouse := targetTasks[datasourceID]; isWarehouse &&
			strings.Contains(err.Error(), "does not match to the provided connection_type") {
			resp.Diagnostics.AddError(
				"Wrong task_type for a warehouse connection",
				fmt.Sprintf(
					"%q is a data-warehouse connector. `task_type` defaults to \"source\", but this "+
						"connection was tested as a source and the API rejected it because a warehouse "+
						"only accepts a \"target\" pull request.\n\n"+
						"Fix: set `task_type = \"target\"` and `task = %q` on this data source, or use "+
						"`boomi_data_integration_target_metadata` instead — it always issues the correct "+
						"`task_type = \"target\"` request and also returns the warehouse's container list "+
						"(databases/datasets/catalogs).\n\nAPI error: %s",
					datasourceID, targetTask, err),
			)
			return
		}
		addAPIError(&resp.Diagnostics, "Error running connection test", err)
		return
	}
	if timedOut {
		resp.Diagnostics.AddWarning(
			"Connection test did not finish in time",
			fmt.Sprintf("%s\n\nThe test is reported as unsuccessful (`success = false`) because the "+
				"result is inconclusive, not because the connection was confirmed broken. The operation "+
				"may still be running on the platform. Re-run to check the outcome, raise "+
				"`timeout_seconds`, or see the data source's documentation for reducing worker "+
				"contention.", err),
		)
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
	if timedOut {
		// result.ErrorMessage is empty on timeout (the operation never reached
		// a terminal state to report a connector error) — surface the timeout
		// detail itself so a postcondition has something actionable to print.
		state.ErrorMessage = types.StringValue(err.Error())
	} else {
		state.ErrorMessage = types.StringValue(result.ErrorMessage)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
