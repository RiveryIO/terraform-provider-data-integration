package provider

import (
	"context"
	"encoding/json"
	"testing"

	fwdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestTargetMetadataSchemaValid(t *testing.T) {
	ds := NewTargetMetadataDataSource()
	resp := &fwdatasource.SchemaResponse{}
	ds.Schema(context.Background(), fwdatasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("target_metadata schema diagnostics: %v", resp.Diagnostics)
	}
	for _, want := range []string{"id", "environment_id", "connection_id", "target_type", "names", "result_json"} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("target_metadata schema missing attribute %q", want)
		}
	}
	if _, ok := resp.Schema.Blocks["timeouts"]; !ok {
		t.Errorf("target_metadata schema missing timeouts block")
	}
}

func TestTargetTasksMapping(t *testing.T) {
	cases := map[string]string{
		"snowflake":  "get_databases",
		"bigquery":   "get_datasets",
		"databricks": "get_catalogs",
	}
	for targetType, wantTask := range cases {
		if got := targetTasks[targetType]; got != wantTask {
			t.Errorf("targetTasks[%q] = %q, want %q", targetType, got, wantTask)
		}
	}
}

// TestExtractNameStrings covers the proven Snowflake shape (flat array of
// strings) plus the defensive array-of-objects and non-array fallbacks.
func TestExtractNameStrings(t *testing.T) {
	// Snowflake get_databases: a flat JSON array of strings.
	var flat any
	if err := json.Unmarshal([]byte(`["AARON","RIVERY_DEMO","SNOWFLAKE_SAMPLE_DATA"]`), &flat); err != nil {
		t.Fatalf("unmarshal flat: %v", err)
	}
	got := extractNameStrings(flat)
	want := []string{"AARON", "RIVERY_DEMO", "SNOWFLAKE_SAMPLE_DATA"}
	if len(got) != len(want) {
		t.Fatalf("flat: got %d names %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("flat[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Array of objects (bigquery/databricks-style) keyed by a name field.
	var objs any
	if err := json.Unmarshal([]byte(`[{"dataset":"analytics"},{"name":"raw"}]`), &objs); err != nil {
		t.Fatalf("unmarshal objs: %v", err)
	}
	got = extractNameStrings(objs)
	if len(got) != 2 || got[0] != "analytics" || got[1] != "raw" {
		t.Errorf("objs: got %v, want [analytics raw]", got)
	}

	// Non-array result yields an empty (non-nil) list; result_json still carries it.
	if got := extractNameStrings(map[string]any{"unexpected": true}); len(got) != 0 {
		t.Errorf("non-array: got %v, want empty", got)
	}
}
