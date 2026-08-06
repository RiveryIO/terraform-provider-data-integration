# Onboarding: Boomi Data Integration Terraform Provider

This guide walks through everything you need to manage Boomi Data Integration
resources as code — from installation to a working data flow.

## What this provider does

The `riveryio/data-integration` provider manages three types of resources in your
Boomi Data Integration account:

| Resource | What it is |
|---|---|
| `boomi_data_integration_connection` | Authenticated link to a source or target system (Jira, Snowflake, MySQL, S3, …) |
| `boomi_data_integration_data_flow` | A data movement job — reads from a connection, writes to another |
| `boomi_data_integration_environment` | A logical grouping of connections and data flows |

Everything else (blueprints, variables, CDC config) builds on these three.

---

## Step 1 — Install the provider

```hcl
terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 1.0"
    }
  }
}
```

Run `terraform init` to download it.

---

## Step 2 — Authenticate

The provider needs three values: an API token, your account ID, and an environment ID.

**Find them in the UI:**

| Value | Where |
|---|---|
| `token` | Settings → API Tokens → Generate |
| `account_id` | Settings → Account → Account ID |
| `environment_id` | Environments page → click the environment → copy ID from the URL |

Set them as environment variables — never put secrets in `.tf` files:

```bash
export DATA_INTEGRATION_API_TOKEN="your-token"
export DATA_INTEGRATION_ACCOUNT_ID="your-account-id"
export DATA_INTEGRATION_ENVIRONMENT_ID="your-env-id"
```

Then declare an empty provider block and Terraform picks them up automatically:

```hcl
provider "boomi" {}
```

Or pass them explicitly via variables if your workflow requires it:

```hcl
provider "boomi" {
  api_url        = "https://api.rivery.io"
  token          = var.api_token
  account_id     = var.account_id
  environment_id = var.environment_id
}
```

---

## Step 3 — Create connections

A connection authenticates to one system. Set `type` to the connector name and
fill in `parameters_json` with the connector's required fields.

**Jira:**

```hcl
resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "ATATT3x..."   # Atlassian API token, not your login password
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

**S3 (file zone):**

```hcl
resource "boomi_data_integration_connection" "s3" {
  name = "S3"
  type = "aws_fz"

  parameters_json = jsonencode({
    aws_access_key    = "AKIA..."
    aws_access_secret = "..."
    region            = "us-east-1"
    bucket_name       = "my-data-bucket"
  })
}
```

> `parameters_json` is **write-only** — the API never returns credential values
> on read. Rotating a credential is as simple as editing the value and running
> `terraform apply`.

To discover all available connection types and their required fields:

```hcl
data "boomi_data_integration_connection_types" "all" {}

output "types" { value = data.boomi_data_integration_connection_types.all.types }
```

---

## Step 4 — Create a data flow

A data flow moves data from a source connection to a target. The source, target,
and table mapping all go inside `properties_json`.

**Minimal example — Jira Issues → Snowflake (full load):**

```hcl
resource "boomi_data_integration_data_flow" "jira_to_snowflake" {
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

Set `activate = true` to have Terraform activate the flow immediately after creation.
Leave it `false` if you want to review it in the UI first.

---

## Step 5 — Apply

```bash
terraform init      # download provider
terraform plan      # preview what will be created
terraform apply     # create the resources
```

---

## Common patterns

### Rolling-window report instead of full load

Change `additional_source_settings` on the table:

```hcl
additional_source_settings = {
  report_type = "predefined"
  time_period = "last_7_days"   # today | week_to_date | last_7_days | last_30_days | …
}
```

### Add a run schedule

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

### Import an existing resource

If a connection or data flow already exists in the UI, import it without recreating:

```bash
terraform import boomi_data_integration_connection.snowflake <env_id>/<connection_id>
terraform import boomi_data_integration_data_flow.jira_to_snowflake <env_id>/<data_flow_id>
```

Find IDs by clicking the resource in the UI — the ID appears in the URL.

### Team state backend

Boomi does not host Terraform state. Set a backend before sharing configs with teammates:

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

| Example | What it shows |
|---|---|
| [`examples/complete-environment/`](examples/complete-environment/) | Jira + Snowflake + S3 — three connections, two flows |
| [`examples/jira-to-snowflake/`](examples/jira-to-snowflake/) | Full load vs. rolling-window predefined report |
| [`examples/cdc/`](examples/cdc/) | MySQL CDC — snapshot-then-stream vs. stream-only |
| [`examples/mysql-incremental-to-snowflake/`](examples/mysql-incremental-to-snowflake/) | Date-range incremental extraction |
| [`examples/logic-flow/`](examples/logic-flow/) | Orchestration: chain flows + SQL steps |
| [`examples/source-to-target/`](examples/source-to-target/) | MySQL → PostgreSQL, multiple tables |

---

## Guides

In-depth reference for each flow type:

- [CDC Data Flows](docs/guides/cdc-data-flows.md) — snapshot + streaming CDC config
- [Blueprint Data Flows](docs/guides/blueprint-data-flows.md) — parameterised recipe flows
- [Logic Data Flows](docs/guides/logic-data-flows.md) — orchestration steps
- [Incremental Extraction](docs/guides/incremental-extraction.md) — date-range and running-number modes
- [Source-to-Target (databases)](docs/guides/source-to-target-databases.md)
- [Source-to-Target (API connectors)](docs/guides/source-to-target-api-connectors.md)
