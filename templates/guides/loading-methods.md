---
page_title: "Loading Methods - boomi Provider"
subcategory: "Loading methods"
description: |-
  How a data flow writes to its target — overwrite, append, or merge.
---

# Loading Methods

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
   [Metadata & Schema](./metadata-and-schema.md#column-selection-modified_columns-is-a-delta).
   This is the column the merge matches existing rows on.
2. **`merge_method`**, naming the actual merge strategy:

| Target family | Accepted `merge_method` values |
| --- | --- |
| Snowflake | `switch_tables`, `delete_insert`, `merge` |
| Other database targets (BigQuery, Redshift, Databricks, Azure Synapse/SQL, PostgreSQL, Athena) | `insert_on_conflict` |

```hcl
target = {
  name           = "postgres_rds"
  connection_id  = boomi_data_integration_connection.pg.id
  loading_method = "merge"
  merge_method   = "insert_on_conflict"
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

See [Extract Methods](./incremental-extraction.md) and
[CDC Data Flows](./cdc-data-flows.md) for the extract side of this pairing.

Shared fields across every warehouse `target` arm, beyond `loading_method`/
`merge_method`: `connection_id`, `table_name`, `target_prefix`,
`single_table_settings`, `file_zone_settings`, `file_path_destination`,
`is_ordered_merge_key`, `order_expression`, `additional_settings` — see
[Source-to-Target: Databases](./source-to-target-databases.md#the-target-union)
for the per-target-type fields on top of these.
