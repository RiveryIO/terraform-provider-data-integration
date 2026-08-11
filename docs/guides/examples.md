---
page_title: "Examples"
description: |-
  Every runnable example in this repository, organized by topic.
---

# Examples

Every example below is a complete configuration under
[`examples/`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples)
in this repository. Clone the repo, fill in `provider {}` (see the
[Authentication](./authentication.md) guide for both auth modes) and any
placeholder values, then `terraform init && terraform plan`.

## Connections

### Jira

```terraform
resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "..." # Atlassian API token — Settings → Security → API Tokens
  })
}
```

### Snowflake

```terraform
resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account_name          = "xy12345.us-east-1"
    username              = "SVC_USER"
    password              = "..."
    default_database_name = "ANALYTICS"
    warehouse             = "COMPUTE_WH"
    default_schema_name   = "PUBLIC"
  })
}
```

### S3 file zone

```terraform
resource "boomi_data_integration_connection" "s3" {
  name = "S3 File Zone"
  type = "aws_fz"

  parameters_json = jsonencode({
    aws_access_key    = "AKIA..."
    aws_access_secret = "..."
    region            = "us-east-1"
    bucket_name       = "my-data-bucket"
  })
}
```

### Discovering a connection type's properties

```terraform
# Discover the full connector catalog (read from the live API).
data "boomi_data_integration_connection_types" "all" {}

# Discover the configurable fields of one type — the field set stays current
# as the API adds connectors/fields, without changing this provider.
data "boomi_data_integration_connection_type" "mysql" {
  connection_type = "mysql"
}

output "connection_type_count" {
  value = length(data.boomi_data_integration_connection_types.all.connection_types)
}

output "mysql_field_ids" {
  value = data.boomi_data_integration_connection_type.mysql.property_names
}

output "mysql_schema_json" {
  value = data.boomi_data_integration_connection_type.mysql.properties_json
}
```

### Discovering source and target catalogs

```terraform
# Discover the source and target catalogs straight from the live API, so a
# source-to-target data flow can be authored against current, valid values
# without hardcoding — new sources/targets appear with no provider release.
data "boomi_data_integration_source_types" "all" {}
data "boomi_data_integration_target_types" "all" {}

output "source_type_count" {
  value = length(data.boomi_data_integration_source_types.all.source_types)
}

output "enabled_source_ids" {
  value = [for s in data.boomi_data_integration_source_types.all.source_types : s.id if s.status == "enabled"]
}

output "target_type_ids" {
  value = [for t in data.boomi_data_integration_target_types.all.target_types : t.target_type]
}
```

## Data flows, by type

### The minimal shape

The smallest possible source-to-target flow — start here.

```terraform
# Minimal data flow: Jira Issues → Snowflake (full load).
# Shows the required structure of properties_json for a source_to_target flow.

resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "..."
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  # Property names come from GET /v1/connections_types/snowflake — account_name
  # and default_database_name, not the console's "account"/"database" labels.
  parameters_json = jsonencode({
    account_name          = "xy12345.us-east-1"
    username              = "SVC_USER"
    password              = "..."
    default_database_name = "ANALYTICS"
    warehouse             = "COMPUTE_WH"
    default_schema_name   = "PUBLIC"
  })
}

resource "boomi_data_integration_data_flow" "jira_issues" {
  name     = "Jira Issues → Snowflake"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      # Jira exposes predefined reports rather than raw tables, so both the
      # source run_type and each table's run_type_and_datasource below are
      # "predefined_report". For an RDBMS source these would be "multi_tables".
      run_type     = "predefined_report"
      cdc_settings = null
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      # loading_method is REQUIRED on every target.
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        # Only "multi_tables" and "predefined_report" are accepted here — this
        # field is the discriminator that selects the table schema.
        run_type_and_datasource = "predefined_report"
        details = {
          table_name                 = "issues"
          target_table               = "jira_issues"
          is_selected                = true
          extract_method             = "all"
          additional_source_settings = { report_type = "full_table" }
        }
      }]
    }]
  })
}
```

### API connectors, full load vs. predefined report

[`jira-to-snowflake`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/jira-to-snowflake)
— both API-connector run types: `report_type = "full_table"` vs a rolling-window predefined report.
See [API connector data flows](./api-connector-data-flows.md).

### Databases (RDBMS sources)

[`source-to-target`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/source-to-target)
— a general RDBMS source-to-target flow. See [Database data flows](./database-data-flows.md).

[`source-to-target-bigquery`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/source-to-target-bigquery)
— modeled on a real integration flow (ECB exchange rates → BigQuery).

### Incremental extraction

[`mysql-incremental-to-snowflake`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/mysql-incremental-to-snowflake)
— backfill from a fixed start date, then track forward on an increment column. See
[Incremental extraction](./incremental-extraction.md).

### Change-data-capture (CDC)

[`cdc`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/cdc) — MySQL
CDC → Snowflake, two variants side by side: snapshot-then-stream and stream-only. See
[CDC data flows](./cdc-data-flows.md).

MongoDB CDC follows the same shape with a different offset type (`resume_token` instead of a binlog
position):

```terraform
# MongoDB change-data-capture -> Snowflake: streams changes instead of
# re-querying the whole collection every run. See the CDC data flows guide
# for the mandatory scheduler and its cron bounds.

resource "boomi_data_integration_connection" "mongodb_source" {
  name = "MongoDB"
  type = "mongodb"

  parameters_json = jsonencode({
    host     = "db.example.com"
    port     = 27017
    database = "app"
    username = "readonly"
    password = "..."
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account_name          = "xy12345.us-east-1"
    username              = "SVC_USER"
    password              = "..."
    default_database_name = "ANALYTICS"
    warehouse             = "COMPUTE_WH"
    default_schema_name   = "PUBLIC"
  })
}

resource "boomi_data_integration_data_flow" "mongodb_cdc" {
  name     = "mongodb-cdc-to-snowflake"
  type     = "source_to_target"
  activate = true

  # CDC flows require an enabled scheduler before they can be created or activated.
  schedule = {
    cron_expression = "*/15 * * * *"
    is_enabled      = true
  }

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mongodb"
      connection_id = boomi_data_integration_connection.mongodb_source.id
      run_type      = "multi_tables"
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake.id
      loading_method = "merge"
      merge_method   = "merge"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
    }
    schemas = [{
      name = "app"
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name           = "orders"
          target_table   = "orders"
          is_selected    = true
          extract_method = "log"
          table_status   = "waiting_for_migration"
          cdc_settings = {
            initiate_table               = true
            overwrite_table_in_migration = false
          }
        }
      }]
    }]
  })
}

# Seeds the starting position the first CDC run resumes from.
resource "boomi_data_integration_data_flow_cdc_config" "mongodb_offset" {
  data_flow_id = boomi_data_integration_data_flow.mongodb_cdc.id

  config_json = jsonencode({
    datasource_type = "mongodb"
    resume_token    = ""
  })
}
```

### Delivering a report by email, not a warehouse

`target_email` is the one target that needs no connection at all — the result lands in the account's
file zone and is emailed as a download link. See [Loading methods](./loading-methods.md).

```terraform
# A report delivered by email instead of loaded into a warehouse.
# `target_email` is the one target that needs no connection at all — the
# result lands in the account's file zone and is emailed as a download link.

resource "boomi_data_integration_connection" "api_source" {
  name = "Example API source"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "..."
  })
}

resource "boomi_data_integration_data_flow" "report_to_email" {
  name     = "weekly-report-to-email"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.api_source.id
      run_type      = "regular"
      additional_settings = {
        report = "issue"
      }
    }
    target = {
      name       = "target_email"
      email_list = ["alerts@example.com"]
    }
    schemas = []
  })
}
```

### Orchestration (logic data flows)

[`logic-flow`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/logic-flow)
— a `type = "logic"` data flow: a `run_once` container chaining a sub-flow, a Python (logicode) step,
and a warehouse SQL step. See [Logic data flows](./logic-data-flows.md).

## One example per resource and data source

Every resource and data source also has a focused, single-purpose example — useful for cloning just
the one block you need. The same example is embedded on that resource or data source's own page.

| Resource | Example |
| --- | --- |
| `boomi_data_integration_connection` | [`resources/data_integration_connection`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_connection) |
| `boomi_data_integration_data_flow` | [`resources/data_integration_data_flow`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_data_flow) |
| `boomi_data_integration_data_flow_variables` | [`resources/data_integration_data_flow_variables`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_data_flow_variables) |
| `boomi_data_integration_data_flow_cdc_config` | [`resources/data_integration_data_flow_cdc_config`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_data_flow_cdc_config) |
| `boomi_data_integration_environment` | [`resources/data_integration_environment`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_environment) |
| `boomi_data_integration_variable` | [`resources/data_integration_variable`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_variable) |
| `boomi_data_integration_blueprint` | [`resources/data_integration_blueprint`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_blueprint) |
| `boomi_data_integration_blueprint_file` | [`resources/data_integration_blueprint_file`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_blueprint_file) |
| `boomi_data_integration_logicode_file` | [`resources/data_integration_logicode_file`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_logicode_file) |
| `boomi_data_integration_dataframe` | [`resources/data_integration_dataframe`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/resources/data_integration_dataframe) |

| Data source | Example |
| --- | --- |
| `boomi_data_integration_connection_test` | [`data-sources/boomi_data_integration_connection_test`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_connection_test) |
| `boomi_data_integration_connection_type` | [`data-sources/boomi_data_integration_connection_type`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_connection_type) |
| `boomi_data_integration_connection_types` | [`data-sources/boomi_data_integration_connection_types`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_connection_types) |
| `boomi_data_integration_source_types` | [`data-sources/boomi_data_integration_source_types`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_source_types) |
| `boomi_data_integration_target_types` | [`data-sources/boomi_data_integration_target_types`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_target_types) |
| `boomi_data_integration_source_metadata` | [`data-sources/boomi_data_integration_source_metadata`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_source_metadata) |
| `boomi_data_integration_target_metadata` | [`data-sources/boomi_data_integration_target_metadata`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_target_metadata) |
| `boomi_data_integration_data_flow_group` | [`data-sources/boomi_data_integration_data_flow_group`](https://github.com/RiveryIO/terraform-provider-data-integration/tree/main/examples/data-sources/boomi_data_integration_data_flow_group) |

## Deprecated

```terraform
# DEPRECATED EXAMPLE
#
# boomi_data_integration_data_flow_run is deprecated and will be removed in a
# future major version. Terraform manages desired state; it does not run flows.
# Trigger runs from the data flow's own schedule (schedulers_json on
# boomi_data_integration_data_flow, or the platform scheduler), or with a direct
# API call from your orchestrator / CI pipeline. Activation stays in Terraform as
# the `activate` attribute on boomi_data_integration_data_flow.
#
# This example is kept only to document the existing resource for configurations
# that still use it.

# Run an existing data flow through Terraform. This models the API's
# imperative activate_river + run actions as a resource (Terraform provider
# Actions require Terraform >= 1.14, so a resource is the portable form).
resource "boomi_data_integration_data_flow_run" "nightly" {
  data_flow_id = var.data_flow_id

  # Change any trigger value to fire another run on the next apply
  # (same pattern as null_resource.triggers).
  triggers = {
    # run on every apply:
    ts = timestamp()
    # or gate on upstream config: config_hash = sha1(jsonencode(local.data_flow_props))
  }
}

variable "data_flow_id" {
  type        = string
  description = "cross_id of the data flow to run."
}

output "run_id" {
  value = boomi_data_integration_data_flow_run.nightly.run_id
}
```

`boomi_data_integration_data_flow_run` is deprecated — kept as a reference for migrating off it. See
[Activation](./activation.md#running-a-flow-vs-activating-it) for why triggering runs from Terraform
isn't the right tool.
