---
page_title: "boomi_variable Resource"
description: |-
  A Data Integration environment variable (key/value), keyed by name.
---

# boomi_variable (Resource)

A Data Integration environment variable. Variables are an environment-scoped
key/value collection; each key is managed as its own `boomi_variable` resource.
Writes **merge** at the API level, so managing one key never disturbs sibling
keys, and multiple `boomi_variable` resources can target the same environment
safely.

## Example Usage

```terraform
resource "boomi_variable" "region" {
  environment_id = boomi_environment.prod.id
  key            = "default_region"
  value          = "us-east-1"
}
```

## Schema

### Required

- `key` (String) Variable key. Must not contain `.` or `$`. Changing it forces a
  new variable.
- `value` (String) Variable value.

### Optional

- `environment_id` (String) Environment this variable belongs to. Falls back to
  the provider-level `environment_id`. Changing it forces a new variable.

### Read-Only

- `id` (String) Resource id. Equals the variable `key`.

## Token scope

The variables API enforces the dedicated scopes `variables:list` (read) and
`variables:edit` (write). A token whose environment grant is only `role:admin`
is **not** sufficient — the token must carry the explicit `variables:*` scopes
for the target environment, or the API returns `403 insufficient permissions`.

## Import

```shell
# environment-qualified
terraform import boomi_variable.region <environment_id>/<key>

# bare key (requires provider-level environment_id)
terraform import boomi_variable.region <key>
```
