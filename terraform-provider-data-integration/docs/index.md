---
page_title: "boomi Provider"
description: |-
  Manage Boomi Data Integration (Rivery) resources as code.
---

# boomi Provider

The `boomi` provider lets teams manage Boomi **Data Integration** resources —
environments, connections, and data flows — as code, planned and applied
through standard Terraform workflows.

> The customer-facing term is **data flow**; the underlying API path is
> `/rivers`. The provider's public surface uses Data Integration terminology
> (`boomi_data_flow`).

## Example Usage

```terraform
terraform {
  required_providers {
    boomi = {
      source = "boomi/data-integration"
    }
  }
}

provider "boomi" {
  # Prefer environment variables for credentials (see below).
}

resource "boomi_environment" "prod" {
  name = "production"
}
```

## Authentication

The provider authenticates with a Data Integration API token (bearer) scoped to
an account. Each value can be set as a provider attribute or an environment
variable; the attribute wins when both are set.

| Attribute        | Environment variable              | Required | Default                 |
|------------------|-----------------------------------|----------|-------------------------|
| `token`          | `DATA_INTEGRATION_API_TOKEN`      | yes      | —                       |
| `account_id`     | `DATA_INTEGRATION_ACCOUNT_ID`     | yes      | —                       |
| `api_url`        | `DATA_INTEGRATION_API_URL`        | no       | `https://api.rivery.io` |
| `environment_id` | `DATA_INTEGRATION_ENVIRONMENT_ID` | no       | —                       |

`environment_id` sets a default environment for environment-scoped resources
(`boomi_connection`, `boomi_data_flow`) that do not declare their own.

## Schema

### Optional

- `api_url` (String) Base URL of the Data Integration API.
- `token` (String, Sensitive) Data Integration API token.
- `account_id` (String) Data Integration account ID.
- `environment_id` (String) Default environment ID for environment-scoped resources.

## State backends

Boomi does not host or manage customer Terraform state. The provider is
compatible with standard Terraform backends — configure your own (`local`,
`s3` with locking, Terraform Cloud, …).
