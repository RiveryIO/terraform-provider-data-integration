---
page_title: "Environments & variables"
subcategory: "Operations & environments"
description: |-
  Managing environments, environment variables, dataflow variables, and data flow groups.
---

# Environments & variables

-> **This provider does not manage users or permission groups.** There is no
`boomi_data_integration_user` or access-control-group resource today. The
only "group" concept here is `boomi_data_integration_data_flow_group` — a
read-only lookup for the organizational folder a data flow lives in within an
environment. It has nothing to do with who can access what.

## Environments

`boomi_data_integration_environment` manages an account-scoped environment —
the container most other resources (`environment_id`) live in.

```hcl
resource "boomi_data_integration_environment" "staging" {
  name        = "Staging"
  description = "Pre-production testing"
}
```

Most other resources in this provider fall back to the provider-level
`environment_id` when their own `environment_id` attribute is omitted — see
[Getting started](./getting-started.md) for setting that default.

## Environment variables

`boomi_data_integration_variable` manages a single key/value pair scoped to
an environment — the same variables referenced as `{VARIABLE_NAME}` in
notification emails, file-zone paths, and elsewhere across this provider's
other resources.

```hcl
resource "boomi_data_integration_variable" "email_group" {
  environment_id = boomi_data_integration_environment.staging.id
  key            = "DEFAULT_EMAIL_GROUP"
  value          = "alerts@example.com"
}
```

`key` cannot contain `.` or `$`, and changing it forces a new resource — a
variable is identified by its key, not a separate ID.

## Dataflow variables

`boomi_data_integration_data_flow_variables` is a different mechanism —
per-data-flow variables (referenced inside that flow's logic/SQL steps, not
shared account-wide like environment variables above), managed as **one
resource per data flow, replace-all semantics**: the full variable set is
written on every apply, and any variable not listed is deleted from the API.

```hcl
resource "boomi_data_integration_data_flow_variables" "issues_flow_vars" {
  environment_id = boomi_data_integration_environment.staging.id
  data_flow_id   = boomi_data_integration_data_flow.jira_issues.id

  variable {
    name  = "lookback_days"
    value = "7"
  }
  variable {
    name           = "allowed_statuses"
    value          = "[\"Open\",\"In Progress\"]"
    is_multi_value = true
  }
  variable {
    name         = "api_secret"
    value        = var.api_secret
    is_encrypted = true
  }
}
```

- `is_multi_value` — the value is a JSON array string (`'[1,2]'`), not a
  scalar.
- `is_encrypted` — provide plaintext on write; the API encrypts it, and state
  stores the ciphertext, not the plaintext you wrote.
- `clear_value_on_start` — whether the runtime resets this variable's value
  at the start of every run (useful for a lookback counter you want to
  recompute each time, rather than persist).

Order in the `variable` block list is preserved as returned by the API.

## Data flow groups (organizational folders, not permissions)

`boomi_data_integration_data_flow_group` looks up an existing group by name
and returns its `id`, for use as `group_id` on a `boomi_data_integration_data_flow`
resource — this is how you file a data flow into a folder in the console's
UI, purely organizational:

```hcl
data "boomi_data_integration_data_flow_group" "etl" {
  environment_id = boomi_data_integration_environment.staging.id
  name           = "ETL Pipelines"
}

resource "boomi_data_integration_data_flow" "jira_issues" {
  environment_id = boomi_data_integration_environment.staging.id
  group_id       = data.boomi_data_integration_data_flow_group.etl.id
  # ...
}
```

There is no resource to *create* a group through this provider — group
creation is UI-only; this data source only looks up groups that already
exist.
