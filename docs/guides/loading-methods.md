---
page_title: "Loading methods"
subcategory: "Extract & load"
description: |-
  How a data flow writes to its target — overwrite, append, or merge.
---

# Loading methods

Every warehouse-style `target` arm (Snowflake, BigQuery, Redshift, Databricks,
Azure Synapse/SQL, PostgreSQL, Athena, …) declares exactly one required
field: `loading_method`. This is the one piece that decides how the target
table is written on every run, independent of which `run_type` or extract
method produced the rows.

## The three modes

`loading_method` is `LoadingMode`: `overwrite`, `append`, `merge`.

```hcl
target = {
  name          = "snowflake"
  connection_id = boomi_data_integration_connection.warehouse.id
  database_name = "ANALYTICS"
  schema_name   = "PUBLIC"
  loading_method = "overwrite"
}
```

- **`overwrite`** — replace the target table's contents on every run. The
  simplest mode; pairs naturally with `extract_method = "all"`.
- **`append`** — add the run's rows to whatever is already in the target,
  without checking for duplicates. Pairs naturally with an incremental or
  log-based (CDC) extract where every row is genuinely new.
- **`merge`** — upsert: match existing rows on a key and update them,
  insert rows that don't match. This is what most CDC and incremental flows
  actually want, since a row extracted twice (or a changed row from a CDC
  stream) shouldn't become a duplicate.

## `merge` needs a key and a `merge_method`

Two things have to be true for `merge` to work:

1. **A key column.** Mark it in `modified_columns` with `is_key = true` — see
   [Schema & column mapping](./metadata-and-schema.md#column-selection-modified_columns-is-a-delta).
   This is the column the merge matches existing rows on.
2. **`merge_method`**, naming the actual merge strategy:

   The accepted values are **per target**, not one shared list:

| Target | Accepted `merge_method` values |
| --- | --- |
| Snowflake, OneLake | `switch_tables`, `delete_insert`, `merge` |
| BigQuery, Redshift, Databricks, Azure SQL | `switch_tables`, `delete_insert`, `merge`, `insert_on_conflict` |
| PostgreSQL | `delete_insert`, `insert_on_conflict` |
| Athena, Azure Synapse Analytics | *(no `merge_method` field — do not set it)* |

`insert_on_conflict` is **not** accepted by Snowflake, and it is the only
non-`delete_insert` option PostgreSQL takes — so a `merge_method` copied from
one target to another is a common source of validation errors.

```hcl
target = {
  name           = "postgres_rds"
  connection_id  = boomi_data_integration_connection.pg.id
  loading_method = "merge"
  merge_method   = "insert_on_conflict" # valid for PostgreSQL; rejected by Snowflake
}
```

## Where this fits alongside extract method

Loading method and extract method are independent choices that combine, not
one deriving from the other:

| Extract method | Typical loading method |
| --- | --- |
| `all` (full load) | `overwrite` |
| `incremental` | `merge` (or `append` if duplicates are acceptable/impossible) |
| `log` (CDC) | `merge` — a changed row needs to update the existing one, not duplicate it |

See [Incremental extraction](./incremental-extraction.md) and
[CDC data flows](./cdc-data-flows.md) for the extract side of this pairing.

## The `target` union

`target` is discriminated on `name` — which destination you're writing to. This is the same union
for every source type (database, API connector, or blueprint); it's documented once here rather than
repeated in each source guide. The API declares only `loading_method` as required per arm, but a
working flow also needs the connection and the destination container.

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

Fields shared by the warehouse arms, beyond `loading_method`/`merge_method`: `connection_id`,
`table_name`, `target_prefix`, `single_table_settings`, `file_zone_settings`,
`file_path_destination`, `is_ordered_merge_key`, `order_expression`, `additional_settings`.
