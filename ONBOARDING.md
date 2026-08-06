# Onboarding: Boomi Data Integration Terraform Provider

Manage Boomi Data Integration resources — connections, data flows, and
environments — as code with Terraform. Plan changes before applying them, track
configuration in version control, and replicate environments reliably.

## What the provider manages

| Resource | What it is |
|---|---|
| `boomi_data_integration_connection` | Authenticated link to a source or target system (Jira, Snowflake, MySQL, S3, …) |
| `boomi_data_integration_data_flow` | A data movement job that reads from one connection and writes to another |
| `boomi_data_integration_environment` | A logical workspace that groups connections and data flows |

---

## Step 1 — Install the provider

Declare the provider in your Terraform configuration:

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

Run `terraform init` to download it from the Terraform Registry.

---

## Step 2 — Authenticate

The provider reads credentials from these environment variables:

| Variable | Description |
|---|---|
| `DATA_INTEGRATION_API_TOKEN` | API token — **Settings → API Tokens → Generate** |
| `DATA_INTEGRATION_ACCOUNT_ID` | Account ID — **Settings → Account → Account ID** |
| `DATA_INTEGRATION_ENVIRONMENT_ID` | Environment ID — **Environments page** → click environment → copy from URL |

---

## Step 3 — Create connections

A connection authenticates to one system. Set `type` to the connector name and
fill `parameters_json` with the required fields.

> `parameters_json` is **write-only**: the API never returns credential values
> on read, so secrets never end up in Terraform state. Rotating a credential
> means editing the value and running `terraform apply`.

See the connection examples:

- [`examples/connection-jira/`](examples/connection-jira/) — Jira Cloud
- [`examples/connection-snowflake/`](examples/connection-snowflake/) — Snowflake
- [`examples/connection-s3/`](examples/connection-s3/) — S3 file zone

To list all available connection types and their required fields, use the
[`connection-discovery`](examples/connection-discovery/) example.

---

## Step 4 — Create a data flow

A data flow moves data from a source connection to a target. The source, target,
and table mapping are passed as `properties_json`.

See [`examples/data-flow-basic/`](examples/data-flow-basic/) for a minimal
Jira → Snowflake full-load flow showing the required structure.

Set `activate = true` to activate the flow immediately on creation, or leave it
`false` and activate from the UI after reviewing.

---

## Step 5 — Apply

```bash
terraform init      # download provider
terraform plan      # preview what will be created
terraform apply     # create the resources
```

---

## Common patterns

### Rolling-window report

To pull a rolling time window instead of a full table, change
`additional_source_settings` on the table:

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

If a connection or data flow was created in the UI, import it into Terraform
state without recreating it:

```bash
terraform import boomi_data_integration_connection.snowflake <env_id>/<connection_id>
terraform import boomi_data_integration_data_flow.jira_issues <env_id>/<data_flow_id>
```

Find IDs by clicking the resource in the UI — the ID appears in the URL.

### Remote state for teams

Boomi does not host Terraform state. Configure a backend before sharing
configurations with teammates:

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
| [`data-flow-basic/`](examples/data-flow-basic/) | Minimal Jira → Snowflake full-load flow |
| [`jira-to-snowflake/`](examples/jira-to-snowflake/) | Full load vs. rolling-window predefined report |
| [`cdc/`](examples/cdc/) | MySQL CDC — snapshot-then-stream and stream-only variants |
| [`mysql-incremental-to-snowflake/`](examples/mysql-incremental-to-snowflake/) | Date-range incremental extraction |
| [`logic-flow/`](examples/logic-flow/) | Orchestration: chain flows and SQL steps |
| [`connection-jira/`](examples/connection-jira/) | Jira connection |
| [`connection-snowflake/`](examples/connection-snowflake/) | Snowflake connection |
| [`connection-s3/`](examples/connection-s3/) | S3 file zone connection |

---

## Guides

In-depth reference for each flow type:

- [CDC Data Flows](docs/guides/cdc-data-flows.md) — snapshot + streaming CDC
- [Blueprint Data Flows](docs/guides/blueprint-data-flows.md) — parameterised recipe flows
- [Logic Data Flows](docs/guides/logic-data-flows.md) — orchestration steps
- [Incremental Extraction](docs/guides/incremental-extraction.md) — date-range and running-number modes
- [Source-to-Target (databases)](docs/guides/source-to-target-databases.md)
- [Source-to-Target (API connectors)](docs/guides/source-to-target-api-connectors.md)
