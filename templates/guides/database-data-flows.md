---
page_title: "Database data flows"
subcategory: "Building data flows"
description: |-
  The properties_json shape for a data flow whose source is a database:
  run_type multi_tables, the per-table details contract, and why
  modified_columns is a delta rather than a column list.
---

# Database data flows

A data flow with an RDBMS source (MySQL, PostgreSQL, SQL Server, Oracle, BigQuery,
Snowflake, …) replicates one or more **tables** per run. That is what distinguishes it from the
single-report shape in
[API connector data flows](./api-connector-data-flows.md).

`properties_json` is passed to the API verbatim, so **nothing in this provider validates the shape
below**. This guide is the contract.

## The envelope

```hcl
resource "boomi_data_integration_data_flow" "sales" {
  name     = "sales-to-warehouse"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"

    source = {
      name          = "mysql"
      run_type      = "multi_tables"
      connection_id = boomi_data_integration_connection.source.id
    }

    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.warehouse.id
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
      loading_method = "overwrite"
    }

    schemas = [{
      name = "sales"
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name           = "customers"
          is_selected    = true
          target_table   = "customers"
          extract_method = "all"
        }
      }]
    }]
  })
}
```

`run_type = "multi_tables"` is what selects this shape. It is also the API's default for `source`, so
omitting it lands here — but set it explicitly.

## `schemas[].tables[].details`

`schemas[]` groups tables by source schema (`name`). Each entry in `tables[]` carries a
`run_type_and_datasource` discriminator — `multi_tables` for database tables — and a required
`details` object.

`details` is the one part of `properties_json` that is **strictly typed**: the API rejects unknown
keys here (`additionalProperties: false`), unlike the free-form settings elsewhere in the blob. Only
`name` is required.

| Field | Type | Notes |
| --- | --- | --- |
| `name` | string | **Required.** The source table name. |
| `is_selected` | bool, default `false` | A table left unselected is not extracted. Set it. |
| `target_table` | string \| null | Destination table name. Defaults to the source name when unset. |
| `extract_method` | `ExtractMethodEnum` \| null | `all`, `incremental`, `log`, `change_tracking`, `system_versioning`. Falls back to the flow-level method when unset. |
| `incremental_field` | string \| null | Only for `incremental`. |
| `date_range` / `running_number` / `epoch` | object \| null | Exactly one, only for `incremental`. |
| `is_custom_incremental` | bool, default `false` | |
| `modified_columns` | array, default `[]` | Column deltas — see below. |
| `table_status` | `TableStatusEnum` \| null | |
| `exporter_chunk_size` | int, default `30000` | Rows per extract chunk. |
| `cdc_settings` | object \| null | Only for `extract_method = "log"`. |
| `change_tracking_settings` | object \| null | Only for `change_tracking`. |
| `system_versioning_settings` | object \| null | Only for `system_versioning`. |
| `additional_source_settings` | union \| null | Discriminated on `source_type`. |
| `additional_target_settings` | union \| null | Discriminated on `target_type`. |

## Full load vs incremental

Full load is `extract_method = "all"` and needs nothing else.

Incremental is `extract_method = "incremental"` plus `incremental_field` and **exactly one** of
`date_range`, `running_number`, `epoch`. Those companion fields, the `date_range` shape, and the
`incremental` vs `increment` spelling trap are documented in
[Incremental extraction](./incremental-extraction.md) — read that before writing an incremental
table, and do not duplicate the contract by hand.

`log` selects change-data-capture, which has its own rules (a mandatory scheduler, an activation
sequence, and an offset resource): see [CDC data flows](./cdc-data-flows.md).

## Column selection: `modified_columns` is a delta

`modified_columns` is **not** the list of columns to replicate — every column
is selected by default, and this field records only the departures (drop,
rename, retype, mark as key). Listing only the columns you *want* is the
common mistake: omitted columns stay selected, so you get them anyway. Full
field table, the discovery data source, and the exact contract live in
[Schema & column mapping](./metadata-and-schema.md#column-selection-modified_columns-is-a-delta).

## The `target` block

`target` is discriminated on `name` — which destination you're writing to — and is the same union
regardless of source type, so it's documented once, in
[Loading methods](./loading-methods.md#the-target-union), rather than repeated per source guide.
