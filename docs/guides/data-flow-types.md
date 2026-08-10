---
page_title: "Choosing a data flow type"
subcategory: "Building data flows"
description: |-
  How to pick the right run_type and guide for your data flow.
---

# Choosing a data flow type

Every `boomi_data_integration_data_flow` picks its shape with one field:
`source.run_type`. This page is the index — it defines the whole enum once,
then points you at the detailed guide for whichever value applies.

## The `run_type` enum

`RunTypeEnum`: `regular`, `predefined_report`, `multi_tables`, `custom_query`,
`custom`, `legacy`. Defaults to `multi_tables`.

| `run_type` | What it's for | Guide |
|---|---|---|
| `multi_tables` | RDBMS tables (MySQL, Postgres, MSSQL, …), including CDC and Blueprint recipe sources | [Database data flows](./database-data-flows.md), [Blueprint data flows](./blueprint-data-flows.md) |
| `regular` | A SaaS/API connector pulling its native object model (e.g. Jira issues) | [API connector data flows](./api-connector-data-flows.md) |
| `predefined_report` | A SaaS/API connector's built-in report endpoint (date-ranged, pre-aggregated) | [API connector data flows](./api-connector-data-flows.md) |
| `custom_query` / `custom` | Hand-written query against a source that supports it | See the connection type's own settings — not covered by a dedicated guide yet |
| `legacy` | Pre-`RunTypeEnum` flows migrated from the classic console; new flows shouldn't set this | — |

## Picking one, by scenario

- **Moving RDBMS tables** (with or without CDC) → `multi_tables`, see
  [Database data flows](./database-data-flows.md). For
  streaming/log-based CDC specifically, see
  [CDC data flows](./cdc-data-flows.md).
- **Hitting a SaaS/API connector** (Jira, Salesforce, …) → `regular` or
  `predefined_report`, see
  [API connector data flows](./api-connector-data-flows.md).
  If the connector needs mandatory Source Settings
  (`is_native = true`), see [API connector required settings](./api-connectors.md)
  first.
- **Recipe-driven, parameterized flows** → Blueprint, `multi_tables` on the
  source side referencing a `recipe_id` — see
  [Blueprint data flows](./blueprint-data-flows.md).
- **Orchestration — chaining flows, running SQL/Python steps** → this isn't a
  `run_type` at all; it's `type = "logic"` on the same resource. See
  [Logic data flows](./logic-data-flows.md).

## Extract method and schema, independent of `run_type`

Once you've picked a `run_type`, two more choices apply on top of it,
independent of which one you chose:

- **How to extract** — `all`, `incremental` (not "increment"), or `log` (CDC),
  see [Incremental extraction](./incremental-extraction.md) and
  [CDC data flows](./cdc-data-flows.md).
- **Which columns, and how they're typed** — see
  [Schema & column mapping](./metadata-and-schema.md).
