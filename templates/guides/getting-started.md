---
page_title: "Getting started"
description: |-
  Step-by-step guide to managing Boomi Data Integration resources with Terraform.
---

# Getting started

## What this provider manages

| Resource | What it is |
|---|---|
| `boomi_data_integration_connection` | Authenticated link to a source or target system (Jira, Snowflake, MySQL, S3, …) |
| `boomi_data_integration_data_flow` | A data movement job — reads from one connection, writes to another |
| `boomi_data_integration_environment` | A logical workspace grouping connections and data flows |

---

## 1 — Install the provider

```hcl
terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 1.0"
    }
  }
}

provider "boomi" {}
```

Run `terraform init` to download the provider from the Terraform Registry.

The quickest path — a Data Integration API token, via environment variables:

| Variable | Description | Where to find it |
|---|---|---|
| `DATA_INTEGRATION_API_TOKEN` | API token | Settings → API Tokens → Generate |
| `DATA_INTEGRATION_ACCOUNT_ID` | Account ID | Settings → Account → Account ID |
| `DATA_INTEGRATION_ENVIRONMENT_ID` | Environment ID | Environments page → click environment → copy from URL |

Already have Boomi Platform credentials instead? The provider can exchange
those for a JWT automatically — see the [Authentication](./authentication.md)
guide for both modes, every parameter, and how the provider decides which one
you mean.

---

## 2 — Create connections

A connection authenticates to one system. `parameters_json` carries the
connector-specific fields (host, credentials, etc.).

`parameters_json` is **write-only** — the provider never reads credential values
back from the API, so secrets do not end up in Terraform state. Rotating a
credential is a `terraform apply` after editing the value.

**Jira:**

```hcl
resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "..."   # Atlassian API token
  })
}
```

**Snowflake:**

```hcl
resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account   = "xy12345.us-east-1"
    username  = "SVC_USER"
    password  = "..."
    database  = "ANALYTICS"
    warehouse = "COMPUTE_WH"
    schema    = "PUBLIC"
  })
}
```

To list all available connection types and their required fields, use the
[`boomi_data_integration_connection_types`](../data-sources/connection_types.md)
data source.

---

## 3 — Create a data flow

A data flow moves data from a source connection to a target. The source, target,
and table mapping are passed as `properties_json`.

```hcl
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
      run_type      = "single_table"
      cdc_settings  = null
      additional_settings = { source_type = "source_to_target" }
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      schema        = "PUBLIC"
      db            = "ANALYTICS"
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "single_table"
        details = {
          name                       = "issues"
          target_table               = "jira_issues"
          is_selected                = true
          is_custom_incremental      = false
          exporter_chunk_size        = 30000
          modified_columns           = []
          incremental_field          = null
          date_range                 = null
          running_number             = null
          epoch                      = null
          change_tracking_settings   = null
          system_versioning_settings = null
          additional_target_settings = null
          cdc_settings               = { initiate_table = null, overwrite_table_in_migration = null }
          additional_source_settings = { report_type = "full_table" }
        }
      }]
    }]
  })
}
```

Set `activate = true` to activate the flow immediately after creation.

---

## 4 — Apply

```bash
terraform init
terraform plan
terraform apply
```

---

## Common patterns

### Rolling-window report

```hcl
additional_source_settings = {
  report_type = "predefined"
  time_period = "last_7_days"   # today | week_to_date | last_7_days | last_30_days | …
}
```

### Run schedule and notifications

```hcl
settings_json = jsonencode({
  run_timeout_seconds = 3600
  notification = {
    failure       = { email = "alerts@example.com", is_enabled = true,  execution_time_limit_seconds = null }
    warning       = { email = "alerts@example.com", is_enabled = false, execution_time_limit_seconds = null }
    run_threshold = { email = "alerts@example.com", is_enabled = false, execution_time_limit_seconds = 0 }
  }
})
```

### Import existing resources

```bash
terraform import boomi_data_integration_connection.snowflake <env_id>/<connection_id>
terraform import boomi_data_integration_data_flow.jira_issues <env_id>/<data_flow_id>
```

Find IDs in the UI — the ID appears in the URL when you click a resource.

### Remote state for teams

```hcl
terraform {
  backend "s3" {
    bucket = "my-tfstate"
    key    = "boomi/production/terraform.tfstate"
    region = "us-east-1"
  }
}
```

---

## Examples

Every runnable example in the repo, organized by topic, is indexed in the
[Examples](./examples.md) guide.

---

## Next steps

- [Authentication](./authentication.md) — both auth modes, every parameter, and how the right one gets chosen
- [Connections](./connections.md) — finding the right properties, keyfile uploads, file-zone linking
- [Choosing a data flow type](./data-flow-types.md) — the `run_type` enum and which guide applies
- [Logic data flows](./logic-data-flows.md) — orchestration steps
- [Incremental extraction](./incremental-extraction.md) — date-range and running-number modes
- [CDC data flows](./cdc-data-flows.md) — snapshot + streaming CDC
- [Loading methods](./loading-methods.md) — overwrite, append, merge
- [Metadata & schema](./metadata-and-schema.md) — column selection and schema discovery
- [Activation](./activation.md) — how `activate` and drift reconciliation work
- [Environments & variables](./environments-and-variables.md) — environments, environment variables, dataflow variables, groups
- [Blueprint data flows](./blueprint-data-flows.md) — parameterised recipe-driven flows
- [Examples](./examples.md) — every runnable example in the repo, indexed by topic
