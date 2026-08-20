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

---

## Notes

- **Import blocks are one-shot** — once a resource is in state, remove the `import {}` block. Leaving it causes a warning on subsequent plans.
