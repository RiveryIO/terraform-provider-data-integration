---
page_title: "boomi_data_integration_data_flow Resource"
description: |-
  A Data Integration data flow (the API calls this a "river").
---

# boomi_data_integration_data_flow (Resource)

A Data Integration data flow. The API calls this a "river"; the provider uses
the customer-facing **data flow** terminology.

The flow definition is supplied as JSON via `properties_json`. This keeps the
resource forward-compatible with the full river schema without re-modelling
every field, and matches the design note's JSON-passthrough strategy.

## Example Usage

```terraform
resource "boomi_data_integration_data_flow" "daily_load" {
  environment_id = boomi_data_integration_environment.prod.id
  name           = "daily-warehouse-load"
  description    = "Loads the daily batch"
  type           = "logic"

  properties_json = jsonencode({
    properties_type = "logic"
    logic_steps = [
      {
        type            = "river"
        name            = "load-step"
        river_id        = var.sub_river_id
        input_variables = {}
      }
    ]
  })
}
```

## Schema

### Required

- `name` (String) Data flow name.
- `properties_json` (String) The river `properties` object as JSON. Must include
  a `properties_type` discriminator and, for logic flows, a non-empty
  `logic_steps` array. Compared **semantically**, so formatting differences do
  not produce diffs.

### Optional

- `environment_id` (String) Environment this data flow belongs to. Falls back to
  the provider-level `environment_id`. Changing it forces a new data flow.
- `kind` (String) River kind. Defaults to `main_river`.
- `type` (String) River type (e.g. `logic`, `src_to_target`). Defaults to `logic`.
- `description` (String) Stored under the API's `metadata.description`
  (a top-level `description` is rejected by the API).
- `settings_json` (String) The river `settings` object as JSON. Defaults to `{}`.
- `group_id` (String) Group (`cross_id`) the data flow belongs to. Required for
  logic flows whose steps use shared warehouse connections (e.g. a Snowflake SQL
  step): without it the platform cannot route the connection through the group and
  the warehouse driver fails at run time with a misleading connection/404 error.
  Falls back to the API-assigned group when unset.

### Read-Only

- `id` (String) Data flow ID (`cross_id`), assigned by the API.

## Read shape vs. write shape

The API's read shape differs from its write shape (the create/update body wraps
the definition under a `properties_type`-discriminated object, requires
`metadata`/`settings`, and rejects server-only fields). The provider normalizes
reads back to the configured shape so refresh and import plan clean. Because
`properties_json` is compared semantically, author it to match what the API
returns; run `terraform plan` after the first apply to confirm an empty diff.

## Import

```shell
terraform import boomi_data_integration_data_flow.daily_load <environment_id>/<data_flow_id>
```
