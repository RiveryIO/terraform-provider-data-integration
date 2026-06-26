---
page_title: "rivery_environment Resource"
description: |-
  A Data Integration environment.
---

# rivery_environment (Resource)

A Data Integration environment. Environments are account-scoped and group
connections and data flows.

## Example Usage

```terraform
resource "rivery_environment" "prod" {
  name        = "production"
  description = "Managed by terraform-provider-rivery"
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
terraform import rivery_environment.prod <environment_id>
```
