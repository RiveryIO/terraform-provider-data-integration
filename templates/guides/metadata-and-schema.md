---
page_title: "Schema & column mapping"
subcategory: "Extract & load"
description: |-
  Column selection, renaming, and retyping, and how to discover a source's schema instead of hand-writing it.
---

# Schema & column mapping

Two related things live here: how to describe which columns a data flow moves
(and how they're renamed/retyped), and how to discover that shape from the
live source instead of writing it by hand.

## Column selection: `modified_columns` is a delta

This is the field most often misread. `modified_columns` is **not** the list
of columns to replicate. Every column the source exposes is selected by
default; `modified_columns` records only the **departures** from that
default — the columns you deselect, rename, retype, or mark as a key.

The API's own wording for the field is: use it "if you want to unselect a
column or change the column name or type."

So a table with 40 columns where you drop one and rename another has exactly
two entries:

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

Listing only the columns you *want* is the common mistake: the columns you
omit stay selected, so you get them anyway.

`name` and `is_selected` are required on every entry; the rest are optional:

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

This same `modified_columns` shape is what you're building (or overriding)
when you use the discovery data source below — it's the one contract, not
two.

## Calculated columns (source expressions)

`modified_columns` can include columns that do not exist in the source table.
Setting `calculated_column_mode = "source"` tells the platform to evaluate
`expression` as a SQL snippet **at the source database** and land the result in
the target column under `alias`.

```hcl
modified_columns = [
  # Regular column — mark as merge key.
  { name = "order_id", type = "INTEGER", is_selected = true, is_key = true },

  # Expression column — injected at the source DB; not a real column.
  {
    name                   = "ingestion_utc_at"
    alias                  = "ingestion_utc_at"
    expression             = "SYSUTCDATETIME()"
    type                   = "TIMESTAMP"
    calculated_column_mode = "source"
    is_selected            = true
    order                  = 1         # position among expression columns (1-based)
    target_type            = "snowflake"
  },
]
```

Key points:

- The column does not need to exist in the source table — the platform injects it
  into every extracted row via the SELECT.
- `expression` is passed verbatim to the source database. Use the SQL dialect that
  database supports (`SYSUTCDATETIME()` for MSSQL, `UTC_TIMESTAMP()` for MySQL,
  `NOW() AT TIME ZONE 'UTC'` for Postgres).
- Expression columns **cannot be merge keys** — `is_key` must be absent or `false`.
- `calculated_column_mode = "target"` evaluates the expression at the warehouse
  instead of the source. The two modes are independent features; `"source"` is the
  common one for metadata enrichment.

Full runnable example: [`examples/calculated-columns`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/calculated-columns).

## Discovering an RDBMS source's schema

`boomi_data_integration_source_metadata` introspects a live RDBMS source
connection and emits a ready-made `schemas[]` block — including per-table
`extract_method` and, when relevant, the incremental field — instead of you
hand-writing the table list:

```hcl
data "boomi_data_integration_source_metadata" "customers_db" {
  connection_id     = boomi_data_integration_connection.mysql_source.id
  datasource        = "mysql"
  schema            = "app"
  extract_method    = "incremental"
  incremental_field = "updated_at"
}
```

Decode its `schemas_json` output straight into the data flow's
`properties_json.schemas` instead of writing the table/column list by hand —
see [Incremental extraction](./incremental-extraction.md#let-the-data-source-write-it-for-you)
for the full decode pattern. Only RDBMS sources are supported today; there is
no equivalent discovery path yet for SaaS/API connectors.

## Discovering a target's containers

`boomi_data_integration_target_metadata` is the parallel discovery mechanism
for the **target** side — it lists the top-level containers a target
warehouse connection can see (Snowflake databases, BigQuery datasets,
Databricks catalogs) so you can pick a valid `database_name`/`dataset_id`
without guessing or logging into the console:

```hcl
data "boomi_data_integration_target_metadata" "snowflake_dbs" {
  connection_id = boomi_data_integration_connection.snowflake.id
  target_type   = "snowflake" # lists databases; "bigquery" lists datasets, "databricks" lists catalogs
}
```

The discovered names come back as a flat list — check them against the
`target` union's field table in
[Loading methods](./loading-methods.md#the-target-union)
before writing the actual `target` block.
