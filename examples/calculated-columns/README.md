# Calculated columns — source expressions

Enrich every extracted row with columns computed at the source database, without touching the source schema.

## What this is

`modified_columns` entries with `calculated_column_mode = "source"` inject a new column into every row at extraction time. The `expression` field is any scalar SQL expression the source database supports — the platform wraps it into the SELECT that pulls the row.

The result lands in the target table under `alias`. The source table never needs to know the column exists.

## When to use source expressions

| Scenario | Expression (MSSQL) |
|---|---|
| Record when a row was extracted | `SYSUTCDATETIME()` |
| Record local server time | `GETDATE()` |
| Tag rows with a static environment label | `'production'` |
| Tag rows with the source database name | `DB_NAME()` |
| Tag rows with the source table name | `'orders'` |

These are the most common patterns. Any scalar expression the source database accepts in a SELECT clause works.

## Key fields

```hcl
{
  name                   = "ingestion_utc_at"   # must match alias for expression columns
  alias                  = "ingestion_utc_at"   # target column name
  expression             = "SYSUTCDATETIME()"   # evaluated at the source DB per row
  type                   = "TIMESTAMP"           # target DDL type
  calculated_column_mode = "source"              # "source" = evaluated at source DB
  is_selected            = true
  order                  = 1                     # position among expression columns (1-based)
  target_type            = "snowflake"           # target connector; used for type mapping
}
```

`calculated_column_mode = "source"` is the only value shown here. The alternative, `"target"`, evaluates the expression at the warehouse side — a different use case not covered by this example.

## Mixing expression columns with regular columns

Expression columns live in the same `modified_columns` list as regular column mappings. In this example, `order_id` is a regular column marked as the merge key (`is_key = true`); the two expression columns add metadata alongside it.

**Expression columns cannot be merge keys.** `is_key` must be absent or false on expression entries.

## Portability across source databases

The `expression` field is passed verbatim to the source database. MSSQL-specific functions (`SYSUTCDATETIME`, `GETDATE`, `DB_NAME`) do not work against MySQL or Postgres. Substitute the equivalent:

| Goal | MSSQL | MySQL | Postgres |
|---|---|---|---|
| UTC datetime | `SYSUTCDATETIME()` | `UTC_TIMESTAMP()` | `NOW() AT TIME ZONE 'UTC'` |
| Local datetime | `GETDATE()` | `NOW()` | `NOW()` |
| Database name | `DB_NAME()` | `DATABASE()` | `current_database()` |
