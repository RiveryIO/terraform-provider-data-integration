---
page_title: "Examples - boomi Provider"
subcategory: "Examples"
description: |-
  Every runnable example in this repository, organized by topic.
---

# Examples

Every example below is a complete, runnable configuration under
[`examples/`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples)
in this repository — clone it, fill in `provider {}` (see
[`examples/provider`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/provider)
for the credential-resolution pattern used throughout), and `terraform init && terraform plan`.

## Connections

| Example | What it shows |
| --- | --- |
| [`connection-jira`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/connection-jira) | A Jira connection |
| [`connection-snowflake`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/connection-snowflake) | A Snowflake connection |
| [`connection-s3`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/connection-s3) | An S3 file-zone connection (`type = "aws_fz"`) |
| [`connection-discovery`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/connection-discovery) | `boomi_data_integration_connection_types`/`connection_type` — discovering a connection type's required properties, see [Connections](./connections.md) |
| [`type-discovery`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/type-discovery) | `boomi_data_integration_source_types`/`target_types` — the catalog data sources, see [Connections](./connections.md#finding-the-right-properties-for-a-target) |

## Data flows, by type

| Example | What it shows |
| --- | --- |
| [`data-flow-basic`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-flow-basic) | Minimal Jira → Snowflake full-load flow — the required `properties_json` shape, see [Data Flow Types](./data-flow-types.md) |
| [`jira-to-snowflake`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/jira-to-snowflake) | Both API-connector run types: `report_type = "full_table"` vs a rolling-window predefined report, see [API Connector Data Flows](./api-connector-data-flows.md) |
| [`source-to-target`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/source-to-target) | A general RDBMS source-to-target flow, see [Database Data Flows](./database-data-flows.md) |
| [`source-to-target-bigquery`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/source-to-target-bigquery) | Modeled on a real integration flow (ECB exchange rates → BigQuery) — `properties_json` decoded from an exported production flow, not hand-written |
| [`mysql-incremental-to-snowflake`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/mysql-incremental-to-snowflake) | Backfill from a fixed start date, then track forward on an increment column, see [Incremental Extraction](./incremental-extraction.md) |
| [`cdc`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/cdc) | MySQL CDC → Snowflake, two variants side by side: snapshot-then-stream (`table_status = "new_table"`) and stream-only from the current binlog position (`table_status = "tracked"`), see [CDC Data Flows](./cdc-data-flows.md) |
| [`logic-flow`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/logic-flow) | A `type = "logic"` data flow — a `run_once` container chaining three step types, see [Logic Data Flows](./logic-data-flows.md) |

## Resources and data sources, one example each

| Example | Resource / data source |
| --- | --- |
| [`resources/data_integration_connection`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_connection) | `boomi_data_integration_connection` |
| [`resources/data_integration_environment`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_environment) | `boomi_data_integration_environment` |
| [`resources/data_integration_variable`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_variable) | `boomi_data_integration_variable` |
| [`resources/data_integration_blueprint`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_blueprint) | `boomi_data_integration_blueprint` |
| [`resources/data_integration_blueprint_file`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_blueprint_file) | `boomi_data_integration_blueprint_file` |
| [`data-sources/boomi_data_integration_connection_test`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_connection_test) | `boomi_data_integration_connection_test` |
| [`data-sources/boomi_data_integration_source_metadata`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_source_metadata) | `boomi_data_integration_source_metadata`, see [Metadata & Schema](./metadata-and-schema.md) |
| [`data-sources/boomi_data_integration_target_metadata`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_target_metadata) | `boomi_data_integration_target_metadata`, see [Metadata & Schema](./metadata-and-schema.md) |

## Deprecated

| Example | Why it's here |
| --- | --- |
| [`data-flow-run`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-flow-run) | `boomi_data_integration_data_flow_run` is deprecated — kept as a reference for migrating off it. See [Activation](./activation.md#running-a-flow-vs-activating-it) for why triggering runs from Terraform isn't the right tool. |
