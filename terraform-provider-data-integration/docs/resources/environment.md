---
page_title: "boomi_environment Resource"
description: |-
  A Data Integration environment.
---

# boomi_environment (Resource)

A Data Integration environment. Environments are account-scoped and group
connections and data flows.

## Example Usage

```terraform
resource "boomi_environment" "prod" {
  name        = "production"
  description = "Managed by terraform-provider-data-integration"
}
```

## Schema

### Required

- `name` (String) Environment name.

### Optional

- `description` (String) Free-text description.

### Read-Only

- `id` (String) Environment ID, assigned by the API.

## Import

```shell
terraform import boomi_environment.prod <environment_id>
```
