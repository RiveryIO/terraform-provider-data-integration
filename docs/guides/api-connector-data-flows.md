---
page_title: "API connector data flows"
subcategory: "Building data flows"
description: |-
  The properties_json shape for a data flow whose source is a SaaS/API
  connector: run_type regular vs predefined_report, when schemas is present,
  and why source.additional_settings is free-form.
---

# API connector data flows

A data flow extracts from one source and loads into one target. This guide
covers the case where the source is a SaaS or API connector (Jira, Shopify, Salesforce, …). For
RDBMS sources see [Database data flows](./database-data-flows.md).

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
will run — see [API connector required settings](./api-connectors.md).

### `run_type`: `regular` vs `predefined_report`

Both pull a named report from the connector. The difference is where the report's per-table
extraction settings live:

- **`regular`** — the report is configured entirely from `source.additional_settings`, and the flow
  writes to the single table named on the `target`.
- **`predefined_report`** — the report additionally carries a per-table `details` block under
  `schemas[]`, so extraction settings (extract method, date window, target table) are set per report
  entity rather than once on the source.

!> **These two are not interchangeable ways of naming a report.** Whichever
`run_type` you pick, the report identifier has to end up in
`source.additional_settings.report` — naming it only as a `predefined_report`
table's `details.table_name` does **not** populate it, and the run fails. See
the trap below before choosing `predefined_report`.

### Trap: the report identifier must reach the worker as `report`

Every connector's own definition declares which key carries the report
identifier, and for the connectors in this family it is `report`
(`"report_key": "report"`). The extraction worker asserts on it:

```
assert report, 'Missing Entity!'
```

If that key is absent the run fails with `Missing Entity!` — an error that names
nothing, points at nothing, and arrives only at run time. The flow applies and
activates cleanly first, because `properties_json` is opaque to the provider and
the API does not validate this either.

The failure mode that produces it: writing a `predefined_report` flow that names
the report as `schemas[].tables[].details.table_name` and nothing else. Nothing
in that path copies the value into `additional_settings.report`, so the saved
flow reaches the worker with no `report` at all.

```hcl
# WRONG — applies, activates, then fails at run time with `Missing Entity!`
source = {
  name          = "jira"
  run_type      = "predefined_report"
  connection_id = boomi_data_integration_connection.jira.id
}
schemas = [{
  name = "no_schema"
  tables = [{
    run_type_and_datasource = "predefined_report"
    details = {
      table_name     = "project" # names the table, NOT the report
      target_table   = "jira_project"
      is_selected    = true
      extract_method = "all"
    }
  }]
}]

# RIGHT — `report` is where the worker looks
source = {
  name          = "jira"
  run_type      = "regular"
  connection_id = boomi_data_integration_connection.jira.id
  additional_settings = {
    connection_type = "jira"
    report          = "project"
    extract_method  = "all"
  }
}
target  = { /* … */ table_name = "jira_project" }
schemas = []
```

To discover a connector's valid report identifiers, read the `reports` map from:

```
GET .../data_source_properties/global_properties?datasource_id=<slug>
```

For Jira that returns 13: `group`, `group_users`, `issue`, `issue_changelogs`,
`issue_fields`, `project`, `project_category`, `project_role`, `project_type`,
`resolution`, `sprint`, `user`, `work_logs`. (The same response's
`cross_reports_predefined` array tells you whether the connector needs mandatory
Source Settings — empty means it does not. See
[API connector required settings](./api-connectors.md).)

### `run_type` cannot be changed on an existing flow

Switching a flow's `run_type` is rejected on update:

```
API error 422: Cannot change run_type from 'multi_tables' to 'regular' during UPDATE.
Delete and recreate the river.
```

Use `terraform apply -replace=boomi_data_integration_data_flow.<name>`.

Note the quoted "from" value is the API's **stored** `run_type`, which may not be
the one you wrote — a flow created as `predefined_report` reports itself as
`multi_tables` here. Don't let the mismatch send you looking for a phantom
third configuration.

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
in [Loading methods](./loading-methods.md). The full set of `name` values and the required fields of
each arm — identical regardless of source type — is documented once, in
[Loading methods](./loading-methods.md#the-target-union).

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
      table_name     = "predefined_project" # names the TABLE, not the report
      target_table   = "jira_project"
      is_selected    = true
      extract_method = "all"
    }
  }]
}]
```

~> `details.table_name` identifies the table; it does **not** supply the
`report` key the extraction worker requires. A flow whose report is named only
here fails at run time with `Missing Entity!` — set
`source.additional_settings.report` as well. See
[the trap above](#trap-the-report-identifier-must-reach-the-worker-as-report).

`no_schema` is the conventional `schemas[].name` for sources that have no database schema concept.

Note that the `regular`/`predefined_report` split above is a **convention observed in working
configurations, not a constraint the API schema enforces**: `schemas` has no conditional requirement
attached to `run_type`, so a `regular` flow that also populates `schemas[]` is not rejected on
shape alone.

## Trap: `extract_method` spelling

On SaaS/predefined-report tables `extract_method` is an untyped free string, so a misspelling such as
`increment` is accepted silently. It is **wrong for database sources**, where the field is
enum-validated. Do not copy an `extract_method` value out of a SaaS example into a database flow —
see [Incremental extraction](./incremental-extraction.md).
