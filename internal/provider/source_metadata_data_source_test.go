package provider

import (
	"context"
	"encoding/json"
	"testing"

	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
)

// cannedMetadata is a get_db_metadata operation.result payload in the nested
// {schema:{table:{columns:[...]}}} shape, with the field-name variants the
// normalizer must tolerate (data_type, is_primary_key, is_nullable).
const cannedMetadata = `{
  "rivery_dev": {
    "customers": {
      "columns": [
        {"name": "id", "data_type": "int", "is_primary_key": true, "is_nullable": false},
        {"name": "email", "type": "varchar", "nullable": true},
        {"name": "created_at", "column_type": "datetime"}
      ]
    },
    "orders": {
      "columns": [
        {"column_name": "order_id", "data_type": "bigint", "pk": true},
        {"name": "amount", "data_type": "decimal", "is_selected": false}
      ]
    }
  }
}`

func TestSourceMetadataSchemaValid(t *testing.T) {
	ds := NewSourceMetadataDataSource()
	resp := &fwdatasource.SchemaResponse{}
	ds.Schema(context.Background(), fwdatasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("source_metadata schema diagnostics: %v", resp.Diagnostics)
	}
	for _, want := range []string{
		"connection_id", "datasource", "schema", "tables", "schemas", "schemas_json",
		"extract_method", "incremental_field", "date_range",
	} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("source_metadata schema missing attribute %q", want)
		}
	}
	if _, ok := resp.Schema.Blocks["timeouts"]; !ok {
		t.Errorf("source_metadata schema missing timeouts block")
	}
}

func TestParseDiscovery(t *testing.T) {
	var result map[string]any
	if err := json.Unmarshal([]byte(cannedMetadata), &result); err != nil {
		t.Fatalf("unmarshal canned metadata: %v", err)
	}
	schemas := parseDiscovery(result)
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	s := schemas[0]
	if s.Name != "rivery_dev" {
		t.Errorf("schema name = %q, want rivery_dev", s.Name)
	}
	// Tables sorted by name: customers, orders.
	if len(s.Tables) != 2 || s.Tables[0].Name != "customers" || s.Tables[1].Name != "orders" {
		t.Fatalf("unexpected tables (want customers, orders): %+v", s.Tables)
	}

	cust := s.Tables[0]
	if len(cust.Columns) != 3 {
		t.Fatalf("customers: expected 3 columns, got %d", len(cust.Columns))
	}
	id := cust.Columns[0]
	if id.Name != "id" || id.Type != "int" || !id.IsKey || id.Nullable {
		t.Errorf("id column normalized wrong: %+v", id)
	}
	email := cust.Columns[1]
	if email.Name != "email" || email.Type != "varchar" || !email.Nullable || email.IsKey {
		t.Errorf("email column normalized wrong: %+v", email)
	}
	// created_at: only column_type given → type from column_type; nullable defaults true; not selected flag absent → selected true.
	created := cust.Columns[2]
	if created.Type != "datetime" || !created.Nullable || !created.IsSelected {
		t.Errorf("created_at column normalized wrong: %+v", created)
	}

	orders := s.Tables[1]
	// order_id via column_name + pk variant.
	if orders.Columns[0].Name != "order_id" || !orders.Columns[0].IsKey {
		t.Errorf("order_id column normalized wrong: %+v", orders.Columns[0])
	}
	// amount is_selected=false must be honored.
	if orders.Columns[1].IsSelected {
		t.Errorf("amount column should be is_selected=false: %+v", orders.Columns[1])
	}
}

func TestBuildSchemasJSON(t *testing.T) {
	var result map[string]any
	if err := json.Unmarshal([]byte(cannedMetadata), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	js, err := buildSchemasJSON(parseDiscovery(result), incrementalSpec{})
	if err != nil {
		t.Fatalf("buildSchemasJSON: %v", err)
	}

	// Decode back and assert the properties.schemas[] shape.
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(js), &decoded); err != nil {
		t.Fatalf("schemas_json is not valid JSON: %v\n%s", err, js)
	}
	if len(decoded) != 1 || decoded[0]["name"] != "rivery_dev" {
		t.Fatalf("unexpected top-level schemas_json: %s", js)
	}
	tables, ok := decoded[0]["tables"].([]any)
	if !ok || len(tables) != 2 {
		t.Fatalf("expected 2 table entries, got: %v", decoded[0]["tables"])
	}
	first := tables[0].(map[string]any)
	if first["run_type_and_datasource"] != "multi_tables" {
		t.Errorf("missing run_type_and_datasource=multi_tables: %v", first)
	}
	details, ok := first["details"].(map[string]any)
	if !ok {
		t.Fatalf("missing details object: %v", first)
	}
	if details["name"] != "customers" {
		t.Errorf("details.name = %v, want customers", details["name"])
	}
	if details["is_selected"] != true {
		t.Errorf("details.is_selected = %v, want true", details["is_selected"])
	}
	if details["target_table"] != "CUSTOMERS" {
		t.Errorf("details.target_table = %v, want CUSTOMERS", details["target_table"])
	}
	if details["extract_method"] != "all" {
		t.Errorf("details.extract_method = %v, want all", details["extract_method"])
	}
	cols, ok := details["modified_columns"].([]any)
	if !ok || len(cols) != 3 {
		t.Fatalf("expected 3 modified_columns, got: %v", details["modified_columns"])
	}
	c0 := cols[0].(map[string]any)
	for _, k := range []string{"name", "type", "is_selected"} {
		if _, ok := c0[k]; !ok {
			t.Errorf("modified_columns[0] missing key %q: %v", k, c0)
		}
	}
	if c0["name"] != "id" || c0["type"] != "int" || c0["is_selected"] != true {
		t.Errorf("modified_columns[0] wrong: %v", c0)
	}
}

// bqMetadata mirrors a BigQuery get_db_metadata result: it ignores the schema
// filter and returns the whole catalog with only the requested table populated,
// and reports nullability via `mode` rather than a boolean.
const bqMetadata = `{
  "AWSBilling": {
    "Users": {
      "schema": "AWSBilling", "db_name": "visionbi-cloud", "db_type": "bigquery",
      "table_name": "Users",
      "columns": [
        {"name": "user_id", "type": "INTEGER", "mode": "REQUIRED", "is_key": true},
        {"name": "email", "type": "STRING", "mode": "NULLABLE", "is_key": false}
      ]
    }
  },
  "OtherDataset": {
    "SomeOtherUsers": {"table_name": "SomeOtherUsers", "columns": []}
  }
}`

func TestBigQueryModeAndFilter(t *testing.T) {
	var result map[string]any
	if err := json.Unmarshal([]byte(bqMetadata), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Without filtering, BigQuery returns both datasets.
	all := parseDiscovery(result)
	if len(all) != 2 {
		t.Fatalf("expected 2 schemas pre-filter, got %d", len(all))
	}
	// Filter to the requested schema+table => just AWSBilling.Users.
	filtered := filterDiscovery(all, "AWSBilling", []string{"Users"})
	if len(filtered) != 1 || filtered[0].Name != "AWSBilling" {
		t.Fatalf("filter did not scope to AWSBilling: %+v", filtered)
	}
	if len(filtered[0].Tables) != 1 || filtered[0].Tables[0].Name != "Users" {
		t.Fatalf("filter did not scope to Users: %+v", filtered[0].Tables)
	}
	cols := filtered[0].Tables[0].Columns
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(cols))
	}
	// user_id: mode REQUIRED => not nullable; is_key true.
	if cols[0].Name != "user_id" || cols[0].Type != "INTEGER" || cols[0].Nullable || !cols[0].IsKey {
		t.Errorf("user_id normalized wrong: %+v", cols[0])
	}
	// email: mode NULLABLE => nullable; not key.
	if cols[1].Name != "email" || !cols[1].Nullable || cols[1].IsKey {
		t.Errorf("email normalized wrong: %+v", cols[1])
	}
}

func TestFilterDiscoveryNoFilter(t *testing.T) {
	in := []discoveredSchema{{Name: "a", Tables: []discoveredTable{{Name: "t"}}}}
	out := filterDiscovery(in, "", nil)
	if len(out) != 1 {
		t.Fatalf("no-filter should pass through, got %d", len(out))
	}
}

func TestMergeDiscoveryResult(t *testing.T) {
	dst := map[string]any{}
	a := map[string]any{"db": map[string]any{"t1": map[string]any{"columns": []any{}}}}
	b := map[string]any{"db": map[string]any{"t2": map[string]any{"columns": []any{}}}}
	mergeDiscoveryResult(dst, a)
	mergeDiscoveryResult(dst, b)
	tables := dst["db"].(map[string]any)
	if len(tables) != 2 || tables["t1"] == nil || tables["t2"] == nil {
		t.Fatalf("merge did not combine tables: %v", tables)
	}
}

// TestBuildSchemasJSONDefaultUnchanged locks schemas_json to its pre-incremental
// byte-for-byte shape when none of the new extract_method/incremental_field/
// date_range inputs are set (zero-value incrementalSpec) — the mandatory
// backward-compatibility requirement for adding incremental support.
func TestBuildSchemasJSONDefaultUnchanged(t *testing.T) {
	var result map[string]any
	if err := json.Unmarshal([]byte(cannedMetadata), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	js, err := buildSchemasJSON(parseDiscovery(result), incrementalSpec{})
	if err != nil {
		t.Fatalf("buildSchemasJSON: %v", err)
	}
	const want = `[{"name":"rivery_dev","tables":[{"details":{"extract_method":"all","is_selected":true,"modified_columns":[{"is_selected":true,"name":"id","type":"int"},{"is_selected":true,"name":"email","type":"varchar"},{"is_selected":true,"name":"created_at","type":"datetime"}],"name":"customers","target_table":"CUSTOMERS"},"run_type_and_datasource":"multi_tables"},{"details":{"extract_method":"all","is_selected":true,"modified_columns":[{"is_selected":true,"name":"order_id","type":"bigint"},{"is_selected":false,"name":"amount","type":"decimal"}],"name":"orders","target_table":"ORDERS"},"run_type_and_datasource":"multi_tables"}]}]`
	if js != want {
		t.Fatalf("schemas_json changed for the default (no incremental inputs) case:\ngot:  %s\nwant: %s", js, want)
	}
}

// TestBuildSchemasJSONIncremental verifies extract_method/incremental_field/
// date_range are stamped onto every table's details when configured, including
// is_custom_incremental=false and the nested split_time_intervals shape.
func TestBuildSchemasJSONIncremental(t *testing.T) {
	var result map[string]any
	if err := json.Unmarshal([]byte(cannedMetadata), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	dateRange := map[string]any{
		"time_period": "year_to_date",
		"start_date":  "2024-01-01T00:00:00.000+0000",
		"split_time_intervals": map[string]any{
			"time_interval": "dont_split",
			"interval_size": int64(1),
		},
	}
	js, err := buildSchemasJSON(parseDiscovery(result), incrementalSpec{
		ExtractMethod:    "incremental",
		IncrementalField: "updated_at",
		DateRange:        dateRange,
	})
	if err != nil {
		t.Fatalf("buildSchemasJSON: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(js), &decoded); err != nil {
		t.Fatalf("schemas_json is not valid JSON: %v\n%s", err, js)
	}
	tables := decoded[0]["tables"].([]any)
	for _, tbl := range tables {
		details := tbl.(map[string]any)["details"].(map[string]any)
		if details["extract_method"] != "incremental" {
			t.Errorf("details.extract_method = %v, want incremental: %v", details["extract_method"], details)
		}
		if details["incremental_field"] != "updated_at" {
			t.Errorf("details.incremental_field = %v, want updated_at: %v", details["incremental_field"], details)
		}
		if details["is_custom_incremental"] != false {
			t.Errorf("details.is_custom_incremental = %v, want false: %v", details["is_custom_incremental"], details)
		}
		dr, ok := details["date_range"].(map[string]any)
		if !ok {
			t.Fatalf("details.date_range missing or wrong type: %v", details)
		}
		if dr["time_period"] != "year_to_date" || dr["start_date"] != "2024-01-01T00:00:00.000+0000" {
			t.Errorf("date_range top-level fields wrong: %v", dr)
		}
		sti, ok := dr["split_time_intervals"].(map[string]any)
		if !ok || sti["time_interval"] != "dont_split" {
			t.Errorf("date_range.split_time_intervals wrong: %v", dr["split_time_intervals"])
		}
	}
}

// TestValidateExtractMethodConfig covers the conflicting-config diagnostic:
// incremental_field/date_range set while extract_method resolves to "all" is
// a config mistake and must be rejected rather than silently producing a
// full-reload mapping.
func TestValidateExtractMethodConfig(t *testing.T) {
	// Valid: "all" with neither incremental_field nor date_range.
	if _, _, _, invalid := validateExtractMethodConfig("all", "", false); invalid {
		t.Errorf("all + no incremental inputs should be valid")
	}
	// Valid: "incremental" with incremental_field set.
	if _, _, _, invalid := validateExtractMethodConfig("incremental", "updated_at", false); invalid {
		t.Errorf("incremental + incremental_field should be valid")
	}
	// Invalid: "all" (explicit) with incremental_field set.
	attr, summary, detail, invalid := validateExtractMethodConfig("all", "updated_at", false)
	if !invalid {
		t.Fatalf("all + incremental_field should be invalid")
	}
	if attr.String() != "incremental_field" {
		t.Errorf("expected error attached to incremental_field, got %q", attr.String())
	}
	if summary == "" || detail == "" {
		t.Errorf("expected non-empty summary/detail, got %q / %q", summary, detail)
	}
	// Invalid: "all" (explicit) with date_range set.
	attr, _, _, invalid = validateExtractMethodConfig("all", "", true)
	if !invalid {
		t.Fatalf("all + date_range should be invalid")
	}
	if attr.String() != "date_range" {
		t.Errorf("expected error attached to date_range, got %q", attr.String())
	}
	// Invalid: unknown extract_method value entirely.
	attr, _, _, invalid = validateExtractMethodConfig("bogus", "", false)
	if !invalid {
		t.Fatalf("bogus extract_method should be invalid")
	}
	if attr.String() != "extract_method" {
		t.Errorf("expected error attached to extract_method, got %q", attr.String())
	}
}
