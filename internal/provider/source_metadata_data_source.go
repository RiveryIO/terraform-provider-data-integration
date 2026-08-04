package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*sourceMetadataDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*sourceMetadataDataSource)(nil)
)

// NewSourceMetadataDataSource is the factory registered with the provider.
func NewSourceMetadataDataSource() datasource.DataSource { return &sourceMetadataDataSource{} }

type sourceMetadataDataSource struct {
	data *providerData
}

// ---- Terraform state models ------------------------------------------------

type smColumnModel struct {
	Name     types.String `tfsdk:"name"`
	Type     types.String `tfsdk:"type"`
	Nullable types.Bool   `tfsdk:"nullable"`
	IsKey    types.Bool   `tfsdk:"is_key"`
}

type smTableModel struct {
	Name    types.String    `tfsdk:"name"`
	Columns []smColumnModel `tfsdk:"columns"`
}

type smSchemaModel struct {
	Name   types.String   `tfsdk:"name"`
	Tables []smTableModel `tfsdk:"tables"`
}

type smTimeoutsModel struct {
	Read types.String `tfsdk:"read"`
}

// smSplitTimeIntervalsModel mirrors the spec's SplitTimeIntervals shape.
type smSplitTimeIntervalsModel struct {
	TimeInterval types.String `tfsdk:"time_interval"`
	IntervalSize types.Int64  `tfsdk:"interval_size"`
}

// smDateRangeModel mirrors the spec's DateRange shape (one of the three
// mutually-exclusive incremental modes, alongside running_number/epoch which
// this data source does not yet expose).
type smDateRangeModel struct {
	TimePeriod                types.String               `tfsdk:"time_period"`
	StartDate                 types.String               `tfsdk:"start_date"`
	EndDate                   types.String               `tfsdk:"end_date"`
	DaysBack                  types.Int64                `tfsdk:"days_back"`
	IncludeEndValue           types.Bool                 `tfsdk:"include_end_value"`
	SplitTimeIntervals        *smSplitTimeIntervalsModel `tfsdk:"split_time_intervals"`
	UpdateIncrementOnFailures types.Bool                 `tfsdk:"update_increment_on_failures"`
	UtcOffset                 types.Int64                `tfsdk:"utc_offset"`
	RoundUp                   types.Bool                 `tfsdk:"round_up"`
}

type sourceMetadataModel struct {
	ID            types.String     `tfsdk:"id"`
	EnvironmentID types.String     `tfsdk:"environment_id"`
	ConnectionID  types.String     `tfsdk:"connection_id"`
	Datasource    types.String     `tfsdk:"datasource"`
	Schema        types.String     `tfsdk:"schema"`
	Tables        types.List       `tfsdk:"tables"`
	Timeouts      *smTimeoutsModel `tfsdk:"timeouts"`

	ExtractMethod    types.String      `tfsdk:"extract_method"`
	IncrementalField types.String      `tfsdk:"incremental_field"`
	DateRange        *smDateRangeModel `tfsdk:"date_range"`

	Schemas     []smSchemaModel `tfsdk:"schemas"`
	SchemasJSON types.String    `tfsdk:"schemas_json"`
}

// extractMethodEnumValues mirrors the spec's ExtractMethodEnum. "all" is the
// full-reload default; the rest are per-table incremental extraction modes.
var extractMethodEnumValues = []string{"all", "incremental", "log", "change_tracking", "system_versioning"}

func isValidExtractMethod(v string) bool {
	for _, e := range extractMethodEnumValues {
		if v == e {
			return true
		}
	}
	return false
}

// validateExtractMethodConfig checks the resolved extract_method (already
// defaulted to "all" by the caller when unset) together with
// incremental_field/date_range, returning the offending attribute path and
// diagnostic summary/detail for the first violation found. invalid is false
// when the combination is valid.
func validateExtractMethodConfig(extractMethod, incrementalField string, dateRangeSet bool) (attr path.Path, summary, detail string, invalid bool) {
	if !isValidExtractMethod(extractMethod) {
		return path.Root("extract_method"), "Invalid extract_method",
			fmt.Sprintf("%q is not a valid ExtractMethodEnum value. Valid values: %s.",
				extractMethod, strings.Join(extractMethodEnumValues, ", ")), true
	}
	if extractMethod == "all" && (incrementalField != "" || dateRangeSet) {
		attr := path.Root("incremental_field")
		if dateRangeSet {
			attr = path.Root("date_range")
		}
		return attr, "incremental_field/date_range set with extract_method \"all\"",
			"incremental_field and date_range only apply to incremental extraction and would silently " +
				"produce a full-reload mapping otherwise. Set extract_method to \"incremental\" (or another " +
				"non-\"all\" ExtractMethodEnum value), or remove incremental_field/date_range to keep a " +
				"full-reload mapping.", true
	}
	return path.Path{}, "", "", false
}

func (d *sourceMetadataDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_source_metadata"
}

func (d *sourceMetadataDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Discovers the schema and columns of an RDBMS source connection, so a " +
			"source-to-target data flow's table/column mapping is discovered from the live source " +
			"rather than hand-written. It drives the same `get_db_metadata` \"pull request\" the " +
			"console's mapping tab uses: the worker fleet opens the connection, introspects the " +
			"requested schema (and, when given, specific tables), and returns the columns. The " +
			"primary output `schemas_json` is a ready-to-use `properties.schemas[]` block — decode it " +
			"with `jsondecode()` into a data flow's `properties_json`. `schemas` exposes the same " +
			"discovery as typed nested objects for inspection. RDBMS sources only (mysql, postgres, " +
			"sqlserver, oracle, …); API/SaaS connector metadata routing is not yet supported.",
		Blocks: map[string]schema.Block{
			"timeouts": schema.SingleNestedBlock{
				Description: "How long to wait for the metadata discovery to finish.",
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
				Description: "Synthetic id: \"<connection_id>:<datasource>:<schema>\".",
			},
			"environment_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Environment ID. Falls back to the provider default.",
			},
			"connection_id": schema.StringAttribute{
				Required:    true,
				Description: "The source connection (cross_id) to introspect.",
			},
			"datasource": schema.StringAttribute{
				Required: true,
				Description: "The source type slug of the connection (e.g. \"mysql\", \"postgres\", " +
					"\"sqlserver\", \"oracle\"). Sent as the pull-request datasource_id.",
			},
			"schema": schema.StringAttribute{
				Optional: true,
				Description: "The source schema/database to introspect (e.g. the MySQL database name). " +
					"Omit to let the source use its connection default.",
			},
			"tables": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Specific tables to discover. Omit to discover all tables in the schema. " +
					"Each named table is introspected with its own metadata pull request and the " +
					"results are merged.",
			},
			"extract_method": schema.StringAttribute{
				Optional: true,
				Description: "The extract_method to stamp onto every discovered table's details in " +
					"schemas_json. One of the RDBMS ExtractMethodEnum values: \"all\", \"incremental\", " +
					"\"log\", \"change_tracking\", \"system_versioning\". Defaults to \"all\" (today's " +
					"full-reload behaviour) when unset. Use \"incremental\" together with " +
					"incremental_field and date_range to generate an incremental mapping instead.",
			},
			"incremental_field": schema.StringAttribute{
				Optional: true,
				Description: "The source column driving the increment (e.g. an updated_at timestamp or " +
					"an auto-increment id). Emitted as every table's details.incremental_field. Only valid " +
					"when extract_method is not \"all\" — setting this with extract_method \"all\" (or " +
					"unset) is a config error; set extract_method to \"incremental\" instead.",
			},
			"date_range": schema.SingleNestedAttribute{
				Optional: true,
				Description: "One of the three mutually-exclusive incremental modes (date_range / " +
					"running_number / epoch — only date_range is exposed here), mirroring the spec's " +
					"DateRange shape. Emitted as every table's details.date_range. Only valid when " +
					"extract_method is not \"all\" — setting this with extract_method \"all\" (or unset) " +
					"is a config error; set extract_method to \"incremental\" instead.",
				Attributes: map[string]schema.Attribute{
					"time_period": schema.StringAttribute{
						Optional: true,
						Description: "RiverTimePeriodEnum value, e.g. \"custom\", \"year_to_date\", " +
							"\"last_7_days\", \"month_to_date\". Use \"custom\" with start_date to backfill " +
							"from a fixed date.",
					},
					"start_date": schema.StringAttribute{
						Optional:    true,
						Description: "RFC3339 date-time. Backfill start when time_period is \"custom\".",
					},
					"end_date": schema.StringAttribute{
						Optional:    true,
						Description: "RFC3339 date-time. Leave unset to track forward indefinitely.",
					},
					"days_back": schema.Int64Attribute{
						Optional:    true,
						Description: "Number of days back to extract, as an alternative to start_date.",
					},
					"include_end_value": schema.BoolAttribute{
						Optional:    true,
						Description: "Whether to include the end_value boundary in the date range.",
					},
					"split_time_intervals": schema.SingleNestedAttribute{
						Optional: true,
						Description: "Splits a large extraction window into chunks to bound per-request " +
							"result size.",
						Attributes: map[string]schema.Attribute{
							"time_interval": schema.StringAttribute{
								Optional: true,
								Description: "IntervalTimeExternalEnum value: \"dont_split\", \"minutes\", " +
									"\"hours\", \"days\", \"weeks\", \"months\", \"years\".",
							},
							"interval_size": schema.Int64Attribute{
								Optional:    true,
								Description: "Number of time_interval units per chunk.",
							},
						},
					},
					"update_increment_on_failures": schema.BoolAttribute{
						Optional: true,
						Description: "Whether to advance the increment marker even when the extraction " +
							"run fails.",
					},
					"utc_offset": schema.Int64Attribute{
						Optional:    true,
						Description: "Offset (in hours) applied to the end date.",
					},
					"round_up": schema.BoolAttribute{
						Optional:    true,
						Description: "Whether to round the end date up to the next interval boundary.",
					},
				},
			},
			"schemas_json": schema.StringAttribute{
				Computed: true,
				Description: "The discovered mapping as a JSON string in the exact " +
					"`properties.schemas[]` shape a source-to-target data flow expects. Feed it into a " +
					"data flow's `properties_json` via `jsondecode(...)`. Each table entry is " +
					"`{run_type_and_datasource:\"multi_tables\", details:{name, is_selected, " +
					"target_table, extract_method:\"all\" (or the configured extract_method), " +
					"modified_columns:[{name,type,is_selected}]}}`.",
			},
			"schemas": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The discovered schemas as typed nested objects, sorted by name.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:    true,
							Description: "Schema/database name.",
						},
						"tables": schema.ListNestedAttribute{
							Computed:    true,
							Description: "Tables in the schema, sorted by name.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										Computed:    true,
										Description: "Table name.",
									},
									"columns": schema.ListNestedAttribute{
										Computed:    true,
										Description: "Columns of the table, in source order.",
										NestedObject: schema.NestedAttributeObject{
											Attributes: map[string]schema.Attribute{
												"name": schema.StringAttribute{
													Computed:    true,
													Description: "Column name.",
												},
												"type": schema.StringAttribute{
													Computed:    true,
													Description: "Source column type.",
												},
												"nullable": schema.BoolAttribute{
													Computed:    true,
													Description: "Whether the column is nullable.",
												},
												"is_key": schema.BoolAttribute{
													Computed:    true,
													Description: "Whether the column is part of the primary key.",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *sourceMetadataDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *sourceMetadataDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config sourceMetadataModel
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

	// extract_method defaults to "all" so schemas_json is byte-for-byte
	// unchanged when none of the new incremental attributes are set.
	extractMethod := config.ExtractMethod.ValueString()
	if extractMethod == "" {
		extractMethod = "all"
	}
	incrementalField := config.IncrementalField.ValueString()
	if attr, summary, detail, invalid := validateExtractMethodConfig(extractMethod, incrementalField, config.DateRange != nil); invalid {
		resp.Diagnostics.AddAttributeError(attr, summary, detail)
		return
	}

	datasourceID := config.Datasource.ValueString()
	schemaName := config.Schema.ValueString()

	// Resolve the optional table list. Empty => discover every table in the schema.
	var tables []string
	if !config.Tables.IsNull() && !config.Tables.IsUnknown() {
		resp.Diagnostics.Append(config.Tables.ElementsAs(ctx, &tables, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// One metadata pull request per requested table (the verified API mechanic
	// takes a single table_name), or a single request with no table_name to
	// discover the whole schema. Results are merged into one nested payload.
	merged := map[string]any{}
	requests := tables
	if len(requests) == 0 {
		requests = []string{""} // single all-tables discovery
	}
	for _, tbl := range requests {
		inputs := map[string]any{"connection_id": config.ConnectionID.ValueString()}
		if schemaName != "" {
			inputs["schemas"] = []string{schemaName}
		}
		if tbl != "" {
			inputs["table_name"] = tbl
		}
		body := map[string]any{
			"task_type":           "source",
			"datasource_id":       datasourceID,
			"task":                "get_db_metadata",
			"pull_request_inputs": inputs,
		}
		result, err := d.data.client.DiscoverSourceMetadata(ctx, envID, body, 4*time.Second, timeout)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Error discovering source metadata", err)
			return
		}
		if result.Status != "D" {
			resp.Diagnostics.AddError("Source metadata discovery failed",
				fmt.Sprintf("get_db_metadata operation %s ended with status %q: %s",
					result.OperationID, result.Status, result.ErrorMessage))
			return
		}
		mergeDiscoveryResult(merged, result.Result)
	}

	// Some sources (notably BigQuery) ignore the schemas input and return the
	// whole catalog with only the requested tables populated, so scope the
	// parsed result to what the practitioner asked for.
	discovered := filterDiscovery(parseDiscovery(merged), schemaName, tables)
	if len(discovered) == 0 {
		raw, _ := json.Marshal(merged)
		resp.Diagnostics.AddError("No tables discovered",
			fmt.Sprintf("The source returned no tables for connection %s (datasource %q, schema %q). Raw result: %s",
				config.ConnectionID.ValueString(), datasourceID, schemaName, truncateForDiag(raw)))
		return
	}

	schemasJSON, err := buildSchemasJSON(discovered, incrementalSpec{
		ExtractMethod:    extractMethod,
		IncrementalField: incrementalField,
		DateRange:        dateRangeToMap(config.DateRange),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error encoding schemas_json", err.Error())
		return
	}

	state := config
	state.EnvironmentID = types.StringValue(envID)
	state.ID = types.StringValue(fmt.Sprintf("%s:%s:%s", config.ConnectionID.ValueString(), datasourceID, schemaName))
	state.SchemasJSON = types.StringValue(schemasJSON)
	state.Schemas = toSchemaModels(discovered)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// ---- Parsing / normalization (ported from discover_schema.py) --------------

// discoveredColumn is a normalized source column.
type discoveredColumn struct {
	Name       string
	Type       string
	Nullable   bool
	IsKey      bool
	IsSelected bool
}

type discoveredTable struct {
	Name    string
	Columns []discoveredColumn
}

type discoveredSchema struct {
	Name   string
	Tables []discoveredTable
}

// mergeDiscoveryResult merges a get_db_metadata result payload (nested
// {schema:{table:{...}}}) into dst, combining schema and table maps.
func mergeDiscoveryResult(dst, src map[string]any) {
	for schemaName, tables := range src {
		tblMap, ok := tables.(map[string]any)
		if !ok {
			dst[schemaName] = tables
			continue
		}
		existing, ok := dst[schemaName].(map[string]any)
		if !ok {
			existing = map[string]any{}
			dst[schemaName] = existing
		}
		for tableName, tbl := range tblMap {
			existing[tableName] = tbl
		}
	}
}

// parseDiscovery converts the raw nested discovery result into sorted schemas /
// tables / normalized columns. Ports discover_schema.py _iter_tables + _norm_column,
// and additionally captures nullable / is_key for the typed output. Ordering is
// made deterministic (schemas and tables sorted by name) for stable plans.
func parseDiscovery(result map[string]any) []discoveredSchema {
	if result == nil {
		return nil
	}
	var schemas []discoveredSchema
	for schemaName, tables := range result {
		tblMap, ok := tables.(map[string]any)
		if !ok {
			continue
		}
		var outTables []discoveredTable
		for tableName, tbl := range tblMap {
			cols := extractColumns(tbl)
			normed := make([]discoveredColumn, 0, len(cols))
			for _, c := range cols {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				normed = append(normed, normColumn(cm))
			}
			outTables = append(outTables, discoveredTable{Name: tableName, Columns: normed})
		}
		sort.Slice(outTables, func(i, j int) bool { return outTables[i].Name < outTables[j].Name })
		schemas = append(schemas, discoveredSchema{Name: schemaName, Tables: outTables})
	}
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].Name < schemas[j].Name })
	return schemas
}

// filterDiscovery scopes the parsed discovery to the requested schema and
// tables. With no schema and no tables it returns the input unchanged. When a
// schema is given, only that schema is kept; when tables are given, only those
// tables are kept and schemas left with no matching table are dropped.
func filterDiscovery(schemas []discoveredSchema, schemaName string, tables []string) []discoveredSchema {
	if schemaName == "" && len(tables) == 0 {
		return schemas
	}
	tableSet := make(map[string]struct{}, len(tables))
	for _, t := range tables {
		tableSet[t] = struct{}{}
	}
	var out []discoveredSchema
	for _, s := range schemas {
		if schemaName != "" && s.Name != schemaName {
			continue
		}
		var kept []discoveredTable
		for _, t := range s.Tables {
			if len(tableSet) > 0 {
				if _, ok := tableSet[t.Name]; !ok {
					continue
				}
			}
			kept = append(kept, t)
		}
		if len(kept) == 0 {
			continue
		}
		out = append(out, discoveredSchema{Name: s.Name, Tables: kept})
	}
	return out
}

// extractColumns pulls the column list out of a table entry, tolerating the
// documented {columns:[...]} shape and near-variants (modified_columns/fields),
// plus a bare list of columns.
func extractColumns(tbl any) []any {
	switch t := tbl.(type) {
	case map[string]any:
		for _, key := range []string{"columns", "modified_columns", "fields"} {
			if v, ok := t[key].([]any); ok {
				return v
			}
		}
	case []any:
		return t
	}
	return nil
}

// normColumn normalizes a raw discovered column, tolerating the field-name
// variants the API uses across connectors.
func normColumn(col map[string]any) discoveredColumn {
	name := firstString(col, "name", "column_name", "field_name")
	ctype := firstString(col, "type", "data_type", "column_type", "target_type")
	if ctype == "" {
		ctype = "STRING"
	}
	isSelected := true
	if v, ok := firstPresent(col, "is_selected", "is_checked"); ok {
		isSelected = isTrue(v)
	}
	// Nullable defaults to true when the source does not report it. BigQuery
	// reports it via `mode` ("REQUIRED"/"NULLABLE"/"REPEATED"); RDBMS sources
	// use a boolean nullable field.
	nullable := true
	if v, ok := firstPresent(col, "mode"); ok {
		if s, ok := v.(string); ok && s != "" {
			nullable = !strings.EqualFold(s, "REQUIRED")
		}
	} else if v, ok := firstPresent(col, "nullable", "is_nullable", "is_null", "allow_null"); ok {
		nullable = isTrue(v)
	}
	isKey := false
	if v, ok := firstPresent(col, "is_key", "is_primary_key", "primary_key", "pk", "key"); ok {
		isKey = isTrue(v)
	}
	return discoveredColumn{Name: name, Type: ctype, Nullable: nullable, IsKey: isKey, IsSelected: isSelected}
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func firstPresent(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

// incrementalSpec carries the optional incremental-extraction settings that
// buildSchemasJSON stamps onto every table's details identically. The zero
// value reproduces today's full-reload output (extract_method "all", no
// incremental_field/date_range/is_custom_incremental keys).
type incrementalSpec struct {
	ExtractMethod    string
	IncrementalField string
	DateRange        map[string]any // nil when the date_range block is absent
}

// dateRangeToMap converts the configured date_range block into the JSON map
// emitted at details.date_range, omitting any sub-fields left unset in
// config. Returns nil when dr is nil (block absent).
func dateRangeToMap(dr *smDateRangeModel) map[string]any {
	if dr == nil {
		return nil
	}
	m := map[string]any{}
	if !dr.TimePeriod.IsNull() && dr.TimePeriod.ValueString() != "" {
		m["time_period"] = dr.TimePeriod.ValueString()
	}
	if !dr.StartDate.IsNull() && dr.StartDate.ValueString() != "" {
		m["start_date"] = dr.StartDate.ValueString()
	}
	if !dr.EndDate.IsNull() && dr.EndDate.ValueString() != "" {
		m["end_date"] = dr.EndDate.ValueString()
	}
	if !dr.DaysBack.IsNull() {
		m["days_back"] = dr.DaysBack.ValueInt64()
	}
	if !dr.IncludeEndValue.IsNull() {
		m["include_end_value"] = dr.IncludeEndValue.ValueBool()
	}
	if dr.SplitTimeIntervals != nil {
		sti := map[string]any{}
		if !dr.SplitTimeIntervals.TimeInterval.IsNull() && dr.SplitTimeIntervals.TimeInterval.ValueString() != "" {
			sti["time_interval"] = dr.SplitTimeIntervals.TimeInterval.ValueString()
		}
		if !dr.SplitTimeIntervals.IntervalSize.IsNull() {
			sti["interval_size"] = dr.SplitTimeIntervals.IntervalSize.ValueInt64()
		}
		if len(sti) > 0 {
			m["split_time_intervals"] = sti
		}
	}
	if !dr.UpdateIncrementOnFailures.IsNull() {
		m["update_increment_on_failures"] = dr.UpdateIncrementOnFailures.ValueBool()
	}
	if !dr.UtcOffset.IsNull() {
		m["utc_offset"] = dr.UtcOffset.ValueInt64()
	}
	if !dr.RoundUp.IsNull() {
		m["round_up"] = dr.RoundUp.ValueBool()
	}
	return m
}

// buildSchemasJSON renders the discovered schemas into the exact
// properties.schemas[] shape a source-to-target data flow expects.
func buildSchemasJSON(schemas []discoveredSchema, inc incrementalSpec) (string, error) {
	extractMethod := inc.ExtractMethod
	if extractMethod == "" {
		extractMethod = "all"
	}
	out := make([]map[string]any, 0, len(schemas))
	for _, s := range schemas {
		tables := make([]map[string]any, 0, len(s.Tables))
		for _, t := range s.Tables {
			details := map[string]any{
				"name":           t.Name,
				"is_selected":    true,
				"target_table":   strings.ToUpper(t.Name),
				"extract_method": extractMethod,
			}
			if inc.IncrementalField != "" {
				details["incremental_field"] = inc.IncrementalField
			}
			if inc.DateRange != nil {
				details["date_range"] = inc.DateRange
			}
			if extractMethod != "all" {
				details["is_custom_incremental"] = false
			}
			if len(t.Columns) > 0 {
				cols := make([]map[string]any, 0, len(t.Columns))
				for _, c := range t.Columns {
					cols = append(cols, map[string]any{
						"name":        c.Name,
						"type":        c.Type,
						"is_selected": c.IsSelected,
					})
				}
				details["modified_columns"] = cols
			}
			tables = append(tables, map[string]any{
				"run_type_and_datasource": "multi_tables",
				"details":                 details,
			})
		}
		out = append(out, map[string]any{"name": s.Name, "tables": tables})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// toSchemaModels maps the discovered schemas into the typed Terraform state model.
func toSchemaModels(schemas []discoveredSchema) []smSchemaModel {
	out := make([]smSchemaModel, 0, len(schemas))
	for _, s := range schemas {
		tables := make([]smTableModel, 0, len(s.Tables))
		for _, t := range s.Tables {
			cols := make([]smColumnModel, 0, len(t.Columns))
			for _, c := range t.Columns {
				cols = append(cols, smColumnModel{
					Name:     types.StringValue(c.Name),
					Type:     types.StringValue(c.Type),
					Nullable: types.BoolValue(c.Nullable),
					IsKey:    types.BoolValue(c.IsKey),
				})
			}
			tables = append(tables, smTableModel{Name: types.StringValue(t.Name), Columns: cols})
		}
		out = append(out, smSchemaModel{Name: types.StringValue(s.Name), Tables: tables})
	}
	return out
}

func truncateForDiag(b []byte) string {
	const max = 800
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
