---
page_title: "boomi_connection Resource"
description: |-
  A Data Integration connection to a data source or target.
---

# boomi_connection (Resource)

A Data Integration connection to a data source or target.

## Example Usage

```terraform
resource "boomi_connection" "warehouse" {
  environment_id = boomi_environment.prod.id
  name           = "snowflake-prod"
  type           = "snowflake"

  parameters_json = jsonencode({
    account  = "xy12345.us-east-1"
    username = "BOOMI_SVC"
    password = var.snowflake_password
    database = "ANALYTICS"
  })
}
```

## Schema

### Required

- `name` (String) Connection name.
- `type` (String) Connection type identifier (e.g. `snowflake`, `postgres`).
  Changing it forces a new connection.

### Optional

- `environment_id` (String) Environment this connection belongs to. Falls back
  to the provider-level `environment_id`. Changing it forces a new connection.
- `parameters_json` (String, Sensitive) Connection-type-specific parameters as a
  JSON object, including credentials.

### Read-Only

- `id` (String) Connection ID, assigned by the API.

## Write-only secrets

`parameters_json` is treated as **write-only**: the Data Integration API omits
credentials on read, so the provider preserves the configured value and never
refreshes it from the API. Rotating a secret by editing `parameters_json` and
re-applying works; the API is the source of truth for everything else.

## Import

```shell
# environment-qualified
terraform import boomi_connection.warehouse <environment_id>/<connection_id>

# bare id (requires provider-level environment_id)
terraform import boomi_connection.warehouse <connection_id>
```

After import, set `parameters_json` in configuration — it cannot be recovered
from the API.
