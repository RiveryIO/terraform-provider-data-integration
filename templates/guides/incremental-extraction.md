---
page_title: "Incremental extraction"
subcategory: "Extract methods"
description: |-
  The per-table extract_method contract for source-to-target data flows: the
  incremental vs increment spelling trap, the incremental_field plus
  exactly-one-of date_range / running_number / epoch rule, and the date_range shape.
---

# Incremental extraction

`boomi_data_integration_data_flow` passes `properties_json` to the API verbatim, so **nothing in
this provider validates the incremental contract**. A wrong value is either rejected by the API or,
worse, accepted and silently extracts the wrong rows. This guide is the contract.

## `extract_method` is per table

Extraction mode is set on every table, at
`properties.schemas[].tables[].details.extract_method`:

```hcl
properties_json = jsonencode({
  properties_type = "source_to_target"
  source          = { name = "mysql", run_type = "multi_tables", connection_id = "…" }
  target          = { name = "postgres_rds", connection_id = "…", loading_method = "merge" }
  schemas = [{
    name = "rivery_dev"
    tables = [{
      details = {
        name           = "orders"
        is_selected    = true
        target_table   = "orders"
        extract_method = "incremental"
        # …companion fields, see below
      }
    }]
  }]
})
```

The API's `ExtractMethodEnum` is:

| Value | Meaning |
| --- | --- |
| `all` | Extract everything, no filter. |
| `incremental` | Filter by an increment field/column. Requires the companion fields below. |
| `log` | Log-based extraction (CDC). See [CDC data flows](../guides/cdc-data-flows). |
| `change_tracking` | SQL Server change tracking. |
| `system_versioning` | SQL Server system-versioned temporal tables. |

## Trap: `incremental`, never `increment`

For **RDBMS sources** `extract_method` is enum-validated server-side and the only correct spelling
is `incremental`.

`increment` is silently tolerated on **SaaS / predefined-report** source tables, because there
`extract_method` is an untyped free string. It is still **wrong for database sources** — do not copy
it out of a SaaS example. The symptom is an API validation error on write for a database source, and
nothing at all for a SaaS source (which is why the bad spelling propagates).

## Companion fields required by `incremental`

Alongside `extract_method = "incremental"`, each table's `details` must carry:

1. `incremental_field` — the source column that drives the increment (an updated-at timestamp, an
   auto-increment id, an epoch column).
2. **Exactly one** of the three mutually exclusive mode objects — `date_range`, `running_number`,
   `epoch`. Set one and only one.
3. `is_custom_incremental = false`.

| Mode object | Use when the increment field is | Fields |
| --- | --- | --- |
| `date_range` | a date/time column | see below |
| `running_number` | a numeric sequence | `start_value`, `end_value` (int or float, nullable), `rows_in_chunk` (int), `include_end_value` (bool) |
| `epoch` | a unix-epoch number | `start_value`, `end_value` (number > 0, nullable), `include_end_value` (bool, default `false`) |

## `date_range` shape

Mirrors the API's `DateRange` schema. Every field is optional; the defaults below are the API's.

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `time_period` | `RiverTimePeriodEnum` \| null | `custom` | `custom`, `yesterday`, `today`, `last_7_days`, `last_365_days`, `week_to_date`, `previous_week`, `previous_week_to_date`, `last_week`, `month_to_date`, `previous_month`, `previous_month_to_date`, `year_to_date` |
| `start_date` | RFC3339 date-time \| null | — | Backfill start. Meaningful when `time_period = "custom"`. |
| `end_date` | RFC3339 date-time \| null | — | Leave `null` to keep tracking forward. |
| `days_back` | int | `0` | |
| `include_end_value` | bool | `false` | Include or exclude `end_date` in the range. |
| `split_time_intervals` | object | — | `time_interval` (`IntervalTimeExternalEnum`: `dont_split`, `minutes`, `hours`, `days`, `weeks`, `months`, `years`; default `dont_split`) and `interval_size` (int, default `1`). With `days` + `3`, each interval extracts three days. |
| `update_increment_on_failures` | bool | `false` | Advance the next run's start date even when the current run fails. |
| `utc_offset` | int \| null | `0` | Offset applied to the end date. |
| `round_up` | bool \| null | — | Round the end date up to the next interval. |

### Backfill from a fixed date, then track forward

`time_period = "custom"` with a `start_date` and `end_date` left `null`:

```hcl
details = {
  name                  = "orders"
  is_selected           = true
  target_table          = "orders"
  extract_method        = "incremental"
  incremental_field     = "updated_at"
  is_custom_incremental = false
  date_range = {
    time_period          = "custom"
    start_date           = "2026-01-01T00:00:00.000Z"
    end_date             = null
    include_end_value    = false
    split_time_intervals = { time_interval = "days", interval_size = 3 }
  }
}
```

## Let the data source write it for you

The `boomi_data_integration_source_metadata` data source can generate this whole incremental mapping
— it takes `extract_method`, `incremental_field` and a `date_range` block and emits them onto every
table it discovers. Decode its `schemas_json` output straight into `properties_json`:

```hcl
data "boomi_data_integration_source_metadata" "orders" {
  connection_id     = boomi_data_integration_connection.source.id
  datasource        = "mysql"
  schema            = "rivery_dev"
  tables            = ["orders"] # omit to discover every table in the schema
  extract_method    = "incremental"
  incremental_field = "updated_at"
  date_range = {
    time_period = "custom"
    start_date  = "2026-01-01T00:00:00.000Z"
  }
}

resource "boomi_data_integration_data_flow" "flow" {
  name = "orders-incremental"
  type = "source_to_target"

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source          = { name = "mysql", run_type = "multi_tables", connection_id = boomi_data_integration_connection.source.id }
    target          = { name = "postgres_rds", connection_id = boomi_data_integration_connection.target.id, loading_method = "merge" }
    schemas         = jsondecode(data.boomi_data_integration_source_metadata.orders.schemas_json)
  })
}
```

Setting `incremental_field` / `date_range` together with `extract_method = "all"` is rejected by the
data source rather than silently ignored.
