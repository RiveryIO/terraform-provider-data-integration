---
page_title: "Source-to-target: API connectors"
subcategory: "Data flow types"
description: |-
  The properties_json shape for a source-to-target data flow whose source is a
  SaaS/API connector: run_type regular vs predefined_report, when schemas is
  present, and why source.additional_settings is free-form.
---

# Source-to-target: API connectors

A **source-to-target** data flow extracts from one source and loads into one target. This guide
covers the case where the source is a SaaS or API connector (Jira, Shopify, Salesforce, …). For
RDBMS sources see [Source-to-target: databases](../guides/source-to-target-databases).

`properties_json` is passed to the API verbatim, so **nothing in this provider validates the shape
below**. This guide is the contract.

## The envelope

```hcl
resource "boomi_data_integration_data_flow" "jira_issues" {
  name     = "jira-issues-to-snowflake"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source          = { /* … */ }
    target          = { /* … */ }
    # schemas — see below
  })
}
```

| Field | Value |
| --- | --- |
| `type` (resource attribute) | `source_to_target` — one of the API's `RiverTypeEnum` values (`source_to_target`, `actions`, `logic`, `connector_executor`). |
| `properties_type` | `source_to_target`. A fixed discriminator. |
| `source` | Required. |
| `target` | Required. |
| `schemas` | Optional, defaults to `[]`. See [When `schemas` is present](#when-schemas-is-present). |

## `source`

Only `name` is required by the API. The fields that matter in practice:

| Field | Notes |
| --- | --- |
| `name` | The connector slug — `jira`, `shopify`, `salesforce`, … The API validates this against a closed list; `boomi_data_integration_source_types` enumerates it. |
| `connection_id` | The `boomi_data_integration_connection` to extract through. |
| `run_type` | `RunTypeEnum`: `regular`, `predefined_report`, `multi_tables`, `custom_query`, `custom`, `legacy`. Defaults to `multi_tables`. API connectors use `regular` or `predefined_report`. |
| `additional_settings` | Connector-specific. Defaults to `{}`. |

Some connectors additionally require a shared block of mandatory Source Settings before any report
will run — see [API connector Source Settings](../guides/api-connectors).

### `run_type`: `regular` vs `predefined_report`

Both pull a named report from the connector. The difference is where the report's per-table
extraction settings live:

- **`regular`** — the report is configured entirely from `source.additional_settings`, and the flow
  writes to the single table named on the `target`.
- **`predefined_report`** — the report additionally carries a per-table `details` block under
  `schemas[]`, so extraction settings (extract method, date window, target table) are set per report
  entity rather than once on the source.

### `additional_settings` is free-form

In the API schema `source.additional_settings` is an untyped object: there are no declared properties
and no validation. Its contents are **connector-specific**, and the only reliable way to learn a
given connector's keys is to configure the source once in the console and read the saved data flow
back over the API.

Keys observed on working Jira flows — illustrative, **not** a general contract:

```hcl
source = {
  name          = "jira"
  connection_id = boomi_data_integration_connection.jira.id
  run_type      = "regular"
  additional_settings = {
    connection_type       = "jira"
    report                = "issue" # which report/endpoint to pull
    extract_method        = "all"
    time_period           = "date_range"
    utc_offset            = 0
    keep_raw_customfields = false
    required_mapping_flag = false
  }
}
```

`report` is the field that selects which endpoint is pulled. It lives inside the untyped
`additional_settings`, so its name and accepted values are connector-defined and not enumerated in
the API schema.

## `target`

`target` is a union discriminated on `name`. The API declares only `loading_method` as required per
arm, but a working flow also needs the connection and the destination container. For a single-table
API-connector flow the destination table is named on the target itself:

```hcl
target = {
  name           = "snowflake"
  connection_id  = boomi_data_integration_connection.warehouse.id
  database_name  = "RIVERY_DEMO"
  schema_name    = "PUBLIC"
  table_name     = "jira_issues"
  loading_method = "overwrite" # LoadingMode: overwrite | append | merge
}
```

`loading_method` (`overwrite`/`append`/`merge`) and, for `merge`, `merge_method` are covered in full
in [Loading Methods](./loading-methods.md).

The full set of `name` values, and the required fields of each arm, is in
[Source-to-target: databases](../guides/source-to-target-databases#the-target-union) — the target
union is identical regardless of source type.

## When `schemas` is present

| `run_type` | `schemas` |
| --- | --- |
| `regular` | Normally absent, or present as `[]`. The single destination table comes from `target.table_name`. |
| `predefined_report` | Present, with one entry per report entity. |

A `predefined_report` entry uses `run_type_and_datasource = "predefined_report"` as its
discriminator, and its `details` block accepts arbitrary extra keys (the API schema sets
`additionalProperties: true`), of which four are declared: `table_name` (the report/table
identifier), `target_table`, `is_selected`, `extract_method`.

```hcl
schemas = [{
  name = "no_schema"
  tables = [{
    run_type_and_datasource = "predefined_report"
    details = {
      table_name     = "predefined_project" # the report identifier
      target_table   = "jira_project"
      is_selected    = true
      extract_method = "all"
    }
  }]
}]
```

`no_schema` is the conventional `schemas[].name` for sources that have no database schema concept.

Note that the `regular`/`predefined_report` split above is a **convention observed in working
configurations, not a constraint the API schema enforces**: `schemas` has no conditional requirement
attached to `run_type`, so a `regular` flow that also populates `schemas[]` is not rejected on
shape alone.

## Trap: `extract_method` spelling

On SaaS/predefined-report tables `extract_method` is an untyped free string, so a misspelling such as
`increment` is accepted silently. It is **wrong for database sources**, where the field is
enum-validated. Do not copy an `extract_method` value out of a SaaS example into a database flow —
see [Incremental extraction](../guides/incremental-extraction).
