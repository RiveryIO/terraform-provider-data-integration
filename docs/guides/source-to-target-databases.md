---
page_title: "Source-to-target: databases"
subcategory: "Data flows"
description: |-
  The properties_json shape for a source-to-target data flow whose source is a
  database: run_type multi_tables, the per-table details contract, the target
  union, and why modified_columns is a delta rather than a column list.
---

# Source-to-target: databases

A source-to-target data flow with an RDBMS source (MySQL, PostgreSQL, SQL Server, Oracle, BigQuery,
Snowflake, …) replicates one or more **tables** per run. That is what distinguishes it from the
single-report API-connector shape in
[Source-to-target: API connectors](../guides/source-to-target-api-connectors).

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
[Incremental extraction](../guides/incremental-extraction) — read that before writing an incremental
table, and do not duplicate the contract by hand.

`log` selects change-data-capture, which has its own rules (a mandatory scheduler, an activation
sequence, and an offset resource): see [CDC data flows](../guides/cdc-data-flows).

## Column selection: `modified_columns` is a delta

This is the field most often misread. `modified_columns` is **not** the list of columns to replicate.
Every column the source exposes is selected by default; `modified_columns` records only the
**departures** from that default — the columns you deselect, rename, retype, or mark as a key.

The API's own wording for the field is: use it "if you want to unselect a column or change the column
name or type."

So a table with 40 columns where you drop one and rename another has exactly two entries:

```hcl
details = {
  name         = "customers"
  is_selected  = true
  target_table = "customers"
  modified_columns = [
    { name = "internal_notes", is_selected = false },
    { name = "email_address", is_selected = true, alias = "EMAIL" },
  ]
}
```

Listing only the columns you *want* is the common mistake: the columns you omit stay selected, so you
get them anyway.

Each item is a union discriminated on `target_type` (`snowflake`, `bigquery`, `redshift`, `athena`,
`azure_sql`, `azure_synapse_analytics`, `databricks`, `postgres_rds`, `s3`, `gcs`, `blob_storage`,
`target_email`). The discriminator has a per-arm default, so working configurations commonly omit it.
`name` and `is_selected` are required; the rest are optional:

| Field | Purpose |
| --- | --- |
| `is_selected` | **Required.** `false` deselects the column. |
| `name` | **Required.** The source column name. |
| `alias` | Rename — the destination column name. |
| `type` | Override the mapped type. |
| `is_key` | Mark as a merge key (needed by `loading_method = "merge"`). |
| `expression` | Makes this a calculated column. |
| `calculated_column_mode` | `source` or `target` — where the expression is evaluated. |
| `order` | Column ordering. |
| `cluster_key` | Clustering key position. |
| `mode` | `NULLABLE`, `REQUIRED`, `REPEATED` (BigQuery). |
| `is_partition` | BigQuery partition column. |

### Let the data source discover the mapping

`boomi_data_integration_source_metadata` introspects the live source and emits a ready-made
`schemas[]` block, including per-table extract settings. Decode it straight into `properties_json`
instead of hand-writing table lists — see
[Incremental extraction](../guides/incremental-extraction#let-the-data-source-write-it-for-you).

## The `target` union

`target` is discriminated on `name`. Each arm declares `loading_method` as its only required field;
everything else is optional in the schema even though a working flow needs the connection and the
destination container.

| `name` | Destination |
| --- | --- |
| `snowflake` | Snowflake — `database_name`, `schema_name` |
| `bigquery` | BigQuery — `dataset_id`, plus `sql_dialect`, `auto_detect_datatype_changes` |
| `redshift` | Redshift |
| `databricks` | Databricks |
| `azure_synapse_analytics` | Azure Synapse Analytics |
| `azure_sql` | Azure SQL |
| `postgres_rds` | PostgreSQL |
| `athena` | Athena |
| `s3` | S3 files |
| `gcs` | Google Cloud Storage files |
| `blob_storage` | Azure Blob Storage files |
| `target_email` | Email delivery |

Fields shared by the warehouse arms: `connection_id`, `loading_method`, `merge_method`, `table_name`,
`target_prefix`, `single_table_settings`, `file_zone_settings`, `file_path_destination`,
`is_ordered_merge_key`, `order_expression`, `additional_settings`.

`loading_method` is `LoadingMode`: `overwrite`, `append`, `merge`. With `merge`, set `merge_method` —
Snowflake accepts `switch_tables`, `delete_insert`, `merge`; the other database targets add
`insert_on_conflict`. The key a merge matches on is the column marked `is_key = true` in
`modified_columns`.
