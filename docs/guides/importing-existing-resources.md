---
page_title: "Importing existing resources"
description: |-
  How to bring existing Boomi Data Integration connections and data flows under Terraform management using import blocks and generate-config-out.
---

# Importing existing resources

If you have connections and data flows already created in the Boomi Data Integration console, you can bring them under Terraform management without recreating them. This guide walks through the full workflow.

---

## Overview

The recommended approach uses Terraform's `import {}` block combined with `terraform plan -generate-config-out`, which pulls the full resource configuration directly from the API — so you don't have to write `properties_json` by hand.

```bash
# 1. Write import blocks (no resource blocks needed yet)
# 2. Init
terraform init

# 3. Pull config from the live API and generate HCL
terraform plan -generate-config-out=generated.tf && sed -i '' '/provider *= *data-integration/d' generated.tf

# 4. Apply the import
terraform apply

# 5. Tidy up: split generated.tf, remove import blocks, verify
terraform plan  # should show: No changes.
```

---

## Why the `sed` command is needed

When Terraform generates `generated.tf`, it adds a `provider` meta-argument to each resource:

```hcl
resource "boomi_data_integration_data_flow" "my_flow" {
  provider = data-integration   # ← generated automatically
  ...
}
```

Terraform derives this label from the resource type prefix: `boomi_data_integration_*` strips `boomi_` and writes `data-integration`. However, the provider's declared local name in `required_providers` is `boomi` — not `data-integration`. When Terraform reads the generated file back, it cannot resolve `data-integration` and fails.

The `sed` strips that line immediately after generation. Without it, `terraform plan` or `terraform apply` fails with:

```
Error: Failed to query available provider packages
Could not retrieve the list of available versions for provider
hashicorp/data-integration: provider registry registry.terraform.io does not
have a provider named registry.terraform.io/hashicorp/data-integration
```

This is a known rough edge in Terraform's config generation feature — it uses the resource type prefix to label the provider rather than the declared local name. The `sed` is a one-line workaround that removes the incorrect label so Terraform falls back to the correct default matching (`boomi_*` resources → `boomi` provider).

---

## MSSQL change tracking

SQL Server's built-in [Change Tracking](https://learn.microsoft.com/en-us/sql/relational-databases/track-changes/about-change-tracking-sql-server) feature records which rows changed since a given version number, without requiring a reliable `updated_at` column or access to the transaction log.

| Decision | Value |
| --- | --- |
| `extract_method` | `change_tracking` |
| `run_type` | `multi_tables` |
| `loading_method` | `merge` |

**How it differs from CDC:** CDC (`extract_method = "log"`) reads the SQL Server transaction log and requires a mandatory enabled scheduler firing every 5 minutes to once a day. Change tracking reads a version-tracked change table instead — simpler to set up, lower overhead, but captures *what* changed rather than a full before/after event stream. Use change tracking when you need efficient incremental sync without the operational overhead of log-based CDC.

**How it differs from incremental:** Incremental extraction (`extract_method = "incremental"`) relies on a watermark column (`updated_at`, `modified_at`) being maintained by the application. Change tracking works at the SQL Server engine level — no watermark column needed, and deletes are captured.

---

## Step-by-step example

### 1. Configure the provider

```hcl
# main.tf
terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 2.0"
    }
  }
}

provider "boomi" {
  api_url        = var.api_url
  token          = var.api_token
  account_id     = var.account_id
  environment_id = var.environment_id
}

variable "api_token"      { type = string; sensitive = true }
variable "api_url"        { type = string; default = "https://api.integration.rivery.in" }
variable "account_id"     { type = string }
variable "environment_id" { type = string }
```

### 2. Write import blocks

Create `import.tf` with one `import {}` block per resource. You only need the resource address and the cross_id — no resource block or `properties_json`:

```hcl
# import.tf
import {
  to = boomi_data_integration_data_flow.my_flow
  id = "<YOUR_RIVER_CROSS_ID>"
}

import {
  to = boomi_data_integration_connection.source_conn
  id = "<YOUR_SOURCE_CONNECTION_ID>"
}

import {
  to = boomi_data_integration_connection.target_conn
  id = "<YOUR_TARGET_CONNECTION_ID>"
}
```

Cross_ids are visible in the Boomi Data Integration console URL or via the API.

### 3. Init and generate config

```bash
terraform init
terraform plan -generate-config-out=generated.tf && sed -i '' '/provider *= *data-integration/d' generated.tf
```

`generated.tf` now contains the full resource blocks — including `properties_json` — fetched from the live API. Review it before proceeding.

#### Example: MSSQL → Snowflake change tracking data flow

After generation, the data flow resource in `generated.tf` looks like this (sensitive values replaced):

```hcl
# __generated__ by Terraform
resource "boomi_data_integration_data_flow" "mssql_snowflake_terraform_dataflow" {
  activate       = true
  environment_id = var.environment_id
  group_id       = var.group_id
  kind           = "main_river"
  name           = "<YOUR_DATAFLOW_NAME>"
  type           = "source_to_target"

  properties_json = jsonencode({
    properties_type = "source_to_target"
    schemas = [{
      name = "<YOUR_SOURCE_SCHEMA>"
      tables = [{
        details = {
          additional_source_settings = {
            extract_method = "change_tracking"
            source_type    = "mssql"
          }
          additional_target_settings = {
            target_type = "snowflake"
          }
          extract_method = "change_tracking"
          is_selected    = true
          name           = "<YOUR_TABLE>"
          target_table   = "<YOUR_TABLE>"
        }
        run_type_and_datasource = "multi_tables"
      }]
    }]
    source = {
      connection_id = boomi_data_integration_connection.mssql.id
      name          = "mssql"
      run_type      = "multi_tables"
    }
    target = {
      connection_id  = boomi_data_integration_connection.snowflake_poc_cdc.id
      database_name  = "<YOUR_SNOWFLAKE_DATABASE>"
      loading_method = "merge"
      schema_name    = "<YOUR_SNOWFLAKE_SCHEMA>"
    }
  })

  schedule = {
    cron_expression = "<YOUR_CRON>"
    is_enabled      = false
  }
}
```

Note how `connection_id` references the imported connection resources by address — not a hardcoded ID. This means Terraform tracks the dependency correctly and the connection cross_ids never appear as literals in your HCL.

### 4. Apply

```bash
terraform apply
```

Terraform imports the resources into state and applies any diff between the generated config and the server.

### 5. Tidy up

1. Split `generated.tf` into named files (e.g. `connection_source.tf`, `dataflow.tf`).
2. Remove the `import {}` blocks from `import.tf` — they are one-shot and Terraform will warn if left in after the resource is already in state.
3. Run `terraform plan` — it should show **No changes**.

```bash
terraform plan
# No changes. Your infrastructure matches the configuration.
```
