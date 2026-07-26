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

type sourceMetadataModel struct {
	ID            types.String     `tfsdk:"id"`
	EnvironmentID types.String     `tfsdk:"environment_id"`
	ConnectionID  types.String     `tfsdk:"connection_id"`
	Datasource    types.String     `tfsdk:"datasource"`
	Schema        types.String     `tfsdk:"schema"`
	Tables        types.List       `tfsdk:"tables"`
	Timeouts      *smTimeoutsModel `tfsdk:"timeouts"`

	Schemas     []smSchemaModel `tfsdk:"schemas"`
	SchemasJSON types.String    `tfsdk:"schemas_json"`
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
			"sqlserver, oracle, …); native/SaaS connector metadata routing is not yet supported.",
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
			"schemas_json": schema.StringAttribute{
				Computed: true,
				Description: "The discovered mapping as a JSON string in the exact " +
					"`properties.schemas[]` shape a source-to-target data flow expects. Feed it into a " +
					"data flow's `properties_json` via `jsondecode(...)`. Each table entry is " +
					"`{run_type_and_datasource:\"multi_tables\", details:{name, is_selected, " +
					"target_table, extract_method:\"all\", modified_columns:[{name,type,is_selected}]}}`.",
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

	schemasJSON, err := buildSchemasJSON(discovered)
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

// buildSchemasJSON renders the discovered schemas into the exact
// properties.schemas[] shape a source-to-target data flow expects.
func buildSchemasJSON(schemas []discoveredSchema) (string, error) {
	out := make([]map[string]any, 0, len(schemas))
	for _, s := range schemas {
		tables := make([]map[string]any, 0, len(s.Tables))
		for _, t := range s.Tables {
			details := map[string]any{
				"name":           t.Name,
				"is_selected":    true,
				"target_table":   strings.ToUpper(t.Name),
				"extract_method": "all",
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
