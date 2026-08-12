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

Prefer to start from a business scenario rather than the mechanics? [Integration patterns by
use case](./use-cases.md) maps common setups — operational DB → Snowflake, ad platforms →
BigQuery, Salesforce → Snowflake, custom REST → Snowflake, CDC replication — to the exact
`run_type`, `extract_method`, and `loading_method` each needs.

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
    credentials_type = "token"
    domain_name      = "yourorg"           # the bare subdomain — NOT a URL
    username         = "user@example.com"
    api_token        = "..."               # Atlassian API token
  })
}
```

Jira's fields are `credentials_type`/`domain_name`/`username`/`api_token`. There
is no `base_url` and no `password` property on this connector —
`credentials_type = "server_app"` uses `full_url`, `username_server_app` and
`password_server_app` instead. Because the connections API **silently drops
unknown `parameters_json` keys**, spelling these wrong applies cleanly and
produces a connection with no credential at all.

**Snowflake:**

```hcl
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

Property names inside `parameters_json` are set by the API, and they often
differ from the vendor's own console labels — Snowflake's is `account_name`,
not `account`, and `default_database_name`, not `database`. To list all
available connection types and their exact required fields, use the
[`boomi_data_integration_connection_types`](../data-sources/data_integration_connection_types.md)
data source.

### Test each connection before you build on it

A connection that applies cleanly is not necessarily a connection that
*works* — `apply` only stores the credentials, it never dials the source.
Attach a [`boomi_data_integration_connection_test`](../data-sources/data_integration_connection_test.md)
to each one with a `postcondition`, and a bad host, credential, or network
path fails the apply immediately:

```hcl
data "boomi_data_integration_connection_test" "snowflake" {
  connection_id = boomi_data_integration_connection.snowflake.id
  datasource_id = "snowflake"

  lifecycle {
    postcondition {
      condition     = self.success
      error_message = "Snowflake connection failed: ${self.error_message}"
    }
  }
}
```

Without this, an unreachable source stays invisible until the data flow's
first run, which surfaces it as a generic timeout roughly ten minutes later
with no indication that the connection was the cause. The test costs a few
seconds. Read the
[data source's page](../data-sources/data_integration_connection_test.md)
before adding it to an existing connection, though — it runs at plan time
there, which has its own consequences.

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
      run_type      = "regular"
      cdc_settings  = null
      additional_settings = {
        connection_type = "jira"
        report          = "issue" # which report to pull — see below
        extract_method  = "all"
      }
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake.id
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
      table_name     = "jira_issues"
    }
    schemas = []
  })
}
```

Four things in there are easy to get wrong and are worth calling out:

- **`report` names which report is pulled, and it must live in
  `source.additional_settings`.** This is the field the extraction worker reads;
  without it a run fails with the opaque `Missing Entity!`. See
  [API connector data flows](./api-connector-data-flows.md#trap-the-report-identifier-must-reach-the-worker-as-report).
- **`run_type_and_datasource`** (used when a flow does populate `schemas[]`) is a
  discriminator accepting exactly two values: `multi_tables` for database
  sources, `predefined_report` for connectors that expose reports. Anything else
  is rejected. It should agree with the source's `run_type`.
- **`loading_method`** is **required** on the target. Omitting it fails
  validation.
- The target's container fields are **`database_name`** and **`schema_name`** —
  not `database`/`schema` or `db`. See
  [Loading methods](./loading-methods.md#the-target-union) for the full target
  union.

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

## Troubleshooting

### The provider behaves nothing like these docs

Wrong error shapes, resources that should exist and don't, or a binary that
clearly isn't the published one — check for a CLI config overriding provider
installation before concluding the provider is broken:

```bash
cat ~/.terraformrc   # look for provider_installation { dev_overrides { ... } }
```

A leftover `dev_overrides` entry for this provider's address silently repoints
every `terraform` invocation at a local binary. There is no warning and no
error; it just behaves oddly.

**Match the address exactly before blaming it.** `dev_overrides` keys are
provider *addresses*, so an entry for a similarly-named but different address
does not affect you. Confirm the key is the address in your `required_providers`
block before acting on it.

To override for one project without editing the shared global file:

```hcl
# .terraformrc.project
provider_installation {
  direct {}
}
```

```bash
export TF_CLI_CONFIG_FILE="$(pwd)/.terraformrc.project"
```

## Examples

Every runnable example in the repo, organized by topic, is indexed in the
[Examples](./examples.md) guide.

---

## Next steps

Start from a scenario: [Integration patterns by use case](./use-cases.md).
Copy a working config: [Examples](./examples.md).

**Authentication & connections**
- [Authentication](./authentication.md) — both auth modes, every parameter, and how the right one gets chosen
- [Connections](./connections.md) — finding the right properties, keyfile uploads, file-zone linking

**Building data flows** — start with [Choosing a data flow type](./data-flow-types.md) for the `run_type` enum and which guide applies, then:
- [Database data flows](./database-data-flows.md), [API connector data flows](./api-connector-data-flows.md), [API connector required settings](./api-connectors.md)
- [Blueprint data flows](./blueprint-data-flows.md), [Logic data flows](./logic-data-flows.md)

**Extract & load** — applies on top of any flow type:
- [Incremental extraction](./incremental-extraction.md), [CDC data flows](./cdc-data-flows.md), [Loading methods](./loading-methods.md), [Schema & column mapping](./metadata-and-schema.md)

**Operations & environments**
- [Activation](./activation.md) — how `activate` and drift reconciliation work
- [Environments & variables](./environments-and-variables.md) — environments, environment variables, dataflow variables, groups
