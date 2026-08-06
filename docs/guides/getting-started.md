---
page_title: "Getting Started - boomi Provider"
description: |-
  Step-by-step guide to managing Boomi Data Integration resources with Terraform.
---

# Getting Started

This guide takes you from zero to a running Terraform-managed data flow in
Boomi Data Integration.

## Prerequisites

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.3
- A Boomi Data Integration account and at least one environment
- An API token (generate from **Settings → API Tokens** in the UI)
- Your **account ID** (Settings → Account) and **environment ID** (Environments page → URL)

## 1 — Configure the provider

Create a `main.tf`:

```terraform
terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 1.0"
    }
  }
}

provider "boomi" {
  api_url        = "https://api.rivery.io"
  token          = var.api_token      # use a variable — never hardcode
  account_id     = var.account_id
  environment_id = var.environment_id
}
```

The provider also reads credentials from environment variables
(`DATA_INTEGRATION_API_TOKEN`, `DATA_INTEGRATION_ACCOUNT_ID`,
`DATA_INTEGRATION_ENVIRONMENT_ID`) — attribute values take precedence when both
are set.

## 2 — Create a connection

Connections authenticate to a data source or target. The `type` field selects
the connector; `parameters_json` carries the connector-specific settings
(host, credentials, etc.).

```terraform
resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake Production"
  type = "snowflake"

  parameters_json = jsonencode({
    account   = var.snowflake_account
    username  = var.snowflake_username
    password  = var.snowflake_password
    database  = var.snowflake_database
    warehouse = var.snowflake_warehouse
  })
}
```

> **Tip:** `parameters_json` is write-only — the API omits credentials on
> read. Terraform preserves your configured value and never refreshes it from
> the API. Rotating a secret is as simple as editing the value and running
> `terraform apply`.

To discover the available connection types and their required fields, use the
[`boomi_data_integration_connection_types`](../data-sources/connection_types.md)
data source or the [connection-discovery example](../../examples/connection-discovery/).

## 3 — Create a data flow

A data flow moves data from a source connection to a target connection. Its
definition is passed as `properties_json` (a JSON string encoding the source,
target, and schemas).

```terraform
resource "boomi_data_integration_data_flow" "daily_load" {
  name     = "Jira Issues → Snowflake"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true   # activate immediately after creation

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
      db            = var.snowflake_database
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "single_table"
        details = {
          name         = "issues"
          target_table = "jira_issues"
          is_selected  = true
          additional_source_settings = { report_type = "full_table" }
          # ... other required fields; see the complete-environment example
        }
      }]
    }]
  })
}
```

## 4 — Add a scheduler

Add a `settings_json` block to run the flow on a schedule:

```terraform
settings_json = jsonencode({
  run_timeout_seconds = 3600
  notification = {
    failure = { email = "alerts@example.com", is_enabled = true, execution_time_limit_seconds = null }
    warning = { email = "alerts@example.com", is_enabled = false, execution_time_limit_seconds = null }
    run_threshold = { email = "alerts@example.com", is_enabled = false, execution_time_limit_seconds = 0 }
  }
})
```

## 5 — Apply

```bash
terraform init
terraform plan    # review what will be created
terraform apply
```

## Importing existing resources

If you have connections or data flows already created in the UI, import them
into Terraform state without recreating them:

```bash
# Import a connection
terraform import boomi_data_integration_connection.snowflake <env_id>/<connection_id>

# Import a data flow
terraform import boomi_data_integration_data_flow.daily_load <env_id>/<data_flow_id>
```

Find resource IDs by clicking the resource in the UI — the ID appears in the URL.

## State backends

Boomi does not host Terraform state. Configure a backend before working in a
team to avoid state conflicts:

```terraform
terraform {
  backend "s3" {
    bucket = "my-tfstate"
    key    = "boomi/integration/terraform.tfstate"
    region = "us-east-1"
  }
}
```

## Next steps

- Browse the guides for all supported flow patterns: [CDC](./cdc-data-flows.md),
  [Blueprint](./blueprint-data-flows.md), [Logic](./logic-data-flows.md),
  [Incremental extraction](./incremental-extraction.md)
- Try the [complete-environment example](../../examples/complete-environment/)
  for a fork-and-fill-in starter template
- Try the [CDC example](../../examples/cdc/) for real-time replication
