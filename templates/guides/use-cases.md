---
page_title: "Integration patterns by use case"
description: |-
  Common source-to-target patterns mapped to the exact run_type, extract_method,
  and loading_method each needs — a scenario-first entry point into the mechanic guides.
---

# Integration patterns by use case

Most data flows fall into a handful of recognizable shapes. This page maps a business scenario
straight to the four provider decisions that define it — `source.type`, `source.run_type`,
`extract_method`, `loading_method` — then hands off to the guide that owns each one in full. Every
claim here links to its source of truth; **this page doesn't restate a contract, it points at one.**
For complete, runnable configurations see [Examples](./examples.md).

## Pick your pattern

| Scenario | Source | Target | `run_type` | `extract_method` | `loading_method` |
| --- | --- | --- | --- | --- | --- |
| [Operational DB to warehouse](#1-operational-database-to-warehouse-bi-and-analytics-engineering) | MySQL / MSSQL / Postgres | Snowflake | `multi_tables` | `incremental` | `merge` |
| [Ad platforms to warehouse](#2-ad-platforms-to-warehouse-marketing-ops) | Facebook/Google/TikTok/LinkedIn Ads | BigQuery / Snowflake | `predefined_report` | rolling `date_range` | `merge` |
| [Salesforce to warehouse](#3-salesforce-to-warehouse-revops-and-pipeline-reporting) | Salesforce | Snowflake | `regular` | `incremental` | `merge` |
| [Custom REST API to warehouse](#4-custom-rest-api-to-warehouse-bespoke-integrations) | Internal/partner REST API | Snowflake | `regular` | varies | `append` or `merge` |
| [Near-real-time replication](#5-near-real-time-replication-cdc) | MySQL / MSSQL / Postgres / MongoDB | Snowflake | `multi_tables` | `log` (CDC) | `merge` |

## 1. Operational database to warehouse (BI and analytics engineering)

**What you're building:** a nightly (or hourly) sync of an application database into the warehouse
your BI tool reads from, without re-extracting the whole table every run.

**The provider shape:** `run_type = "multi_tables"`, one `details` entry per table with
`extract_method = "incremental"` driven by an `updated_at`-style column, loaded with
`loading_method = "merge"` keyed on the table's primary key. Don't reach for `overwrite` once a table
is large enough that a full extract is slow — `merge` only touches changed rows.

**Sketch:**

```hcl
source = { name = "mysql", run_type = "multi_tables", connection_id = boomi_data_integration_connection.db.id }
target = { name = "snowflake", connection_id = boomi_data_integration_connection.wh.id, loading_method = "merge", merge_method = "merge" }
schemas = [{
  name = "app"
  tables = [{
    run_type_and_datasource = "multi_tables"
    details = {
      name = "orders", target_table = "orders", is_selected = true
      extract_method = "incremental", incremental_field = "updated_at"
      date_range = { time_period = "custom", start_date = "2026-01-01T00:00:00.000Z" }
    }
  }]
}]
```

Full runnable configs: [`examples/mysql-incremental-to-snowflake`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/mysql-incremental-to-snowflake),
[`examples/source-to-target`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/source-to-target).

**Read next:** [Database data flows](./database-data-flows.md), [Incremental extraction](./incremental-extraction.md),
[Loading methods](./loading-methods.md), [Schema & column mapping](./metadata-and-schema.md).

## 2. Ad platforms to warehouse (marketing ops)

**What you're building:** a recurring pull of ad spend/performance data for reporting or attribution.

**The provider shape:** `run_type = "predefined_report"` against the platform's built-in report
endpoint, with a rolling `date_range.time_period` (`last_7_days`, `last_30_days`, …) rather than a
fixed window, loaded with `loading_method = "merge"` keyed on date + campaign.

The rolling window matters for a reason specific to this scenario: **ad platforms retroactively
revise attributed conversions** for several days after a click. A one-time full extract or a plain
`append` will miss those revisions or duplicate rows. Re-pulling the last N days on every run and
merging on the natural key (date + campaign/ad ID) is what keeps the numbers correct as the platform
finishes attributing.

**Sketch:**

```hcl
source = {
  name = "google_ads", connection_id = boomi_data_integration_connection.ads.id
  run_type = "predefined_report"
  additional_settings = { report = "campaign_performance", time_period = "last_30_days" }
}
target = { name = "bigquery", connection_id = boomi_data_integration_connection.wh.id, loading_method = "merge", merge_method = "insert_on_conflict" }
```

Full runnable config: [`examples/source-to-target-bigquery`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/source-to-target-bigquery)
(also documents the BigQuery keyfile-upload requirement).

**Read next:** [API connector data flows](./api-connector-data-flows.md), [API connector required
settings](./api-connectors.md), [Loading methods](./loading-methods.md).

## 3. Salesforce to warehouse (RevOps and pipeline reporting)

**What you're building:** a warehouse copy of Salesforce objects (Opportunities, Accounts, …) for
revenue/pipeline dashboards.

**The provider shape:** `run_type = "regular"` (Salesforce's native object model, not a report),
`extract_method = "incremental"` on `SystemModstamp`, loaded with `loading_method = "merge"` keyed
on `Id`.

One gotcha specific to CRM sources: Salesforce soft-deletes records (`IsDeleted = true`) rather than
removing them, and a plain `merge` on `Id` never drops a row — a merged table can only grow. If a
deleted record must disappear from the warehouse, that has to be handled explicitly (a periodic
`overwrite` reconciliation, or filtering `IsDeleted` downstream); the merge contract by itself won't
do it.

**Sketch:**

```hcl
source = { name = "salesforce", run_type = "regular", connection_id = boomi_data_integration_connection.sf.id }
target = { name = "snowflake", connection_id = boomi_data_integration_connection.wh.id, loading_method = "merge", merge_method = "merge" }
schemas = [{
  name = "no_schema"
  tables = [{
    run_type_and_datasource = "predefined_report"
    details = {
      table_name = "opportunity", target_table = "opportunities", is_selected = true
      extract_method = "incremental", incremental_field = "SystemModstamp"
    }
  }]
}]
```

**Read next:** [API connector data flows](./api-connector-data-flows.md), [Incremental
extraction](./incremental-extraction.md), [Loading methods](./loading-methods.md).

## 4. Custom REST API to warehouse (bespoke integrations)

**What you're building:** a data flow against an internal or partner API that has no dedicated
connector — the single most common source shape in production.

**The provider shape:** `run_type = "regular"`, with pagination, auth headers, and endpoint
selection living in `source.additional_settings` (free-form by design — see [API connector data
flows](./api-connector-data-flows.md#additional_settings-is-free-form)). Whether the target uses
`append` or `merge` depends on whether the endpoint exposes a stable record key: no key means
`append` and accept the extract as an event log; a stable key means `merge` like any other
incremental source.

**When to stop hand-rolling `rest` and use a Blueprint instead:** once the same REST shape gets
copy-pasted across several data flows that only differ by a parameter (an account ID, a date
window, an endpoint path), that's the signal to parameterize it once as a
[Blueprint](./blueprint-data-flows.md) and reuse the recipe, instead of maintaining N near-identical
`rest` flows by hand.

**Read next:** [API connector data flows](./api-connector-data-flows.md), [API connector required
settings](./api-connectors.md), [Blueprint data flows](./blueprint-data-flows.md).

## 5. Near-real-time replication (CDC)

**What you're building:** a warehouse copy that stays minutes-fresh instead of waiting for the next
batch run — reading the source's change log instead of re-querying tables.

**The provider shape:** `extract_method = "log"` per table, which brings two rules no other pattern
has: a **mandatory enabled scheduler** (the API refuses to create or enable a CDC flow without one,
and its cron must fire between every 5 minutes and once a day), and an **offset** — the log position
the next run resumes from — managed separately via
[`boomi_data_integration_data_flow_cdc_config`](../resources/data_integration_data_flow_cdc_config).
Loaded with `loading_method = "merge"`, since a changed row from the log must update the existing
row, not duplicate it.

Full runnable configs: [`examples/mongodb-cdc-to-snowflake`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/mongodb-cdc-to-snowflake),
[`examples/cdc`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/cdc)
(MySQL, both the snapshot-then-stream and stream-only variants).

**Read next:** [CDC data flows](./cdc-data-flows.md), [Activation](./activation.md).

## Other common pairs

These reuse one of the five patterns above — the table just points at which one.

| Source → target | Reuses |
| --- | --- |
| Google Sheets → Snowflake | Pattern 1 (usually `extract_method = "all"` + `overwrite` — sheets rarely carry a reliable increment column) |
| Adobe Analytics / Google Analytics → Redshift or Snowflake | Pattern 2 (report-based, rolling window) |
| NetSuite → Snowflake (finance ops reporting) | Pattern 1 or 3, depending on whether the NetSuite connector exposes native objects or predefined reports |
| S3 / SFTP → warehouse | Pattern 1 shape (`multi_tables`-style, full or incremental depending on whether files are replaced or appended at the source) |
