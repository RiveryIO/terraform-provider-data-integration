---
page_title: "API connector required settings"
subcategory: "Building data flows"
description: |-
  How to supply an API connector's required Source Settings in a data flow's
  properties_json, and how to discover which settings are mandatory for a given
  connector.
---

# API connector required settings

Some SaaS sources drive every one of their reports from a shared set of "Source Settings" — the
fields the console shows under *Source Settings — Connector settings applied to every report*, marked
with a red asterisk. GitHub is the canonical example: nothing works until you name an organization
and repositories.

Those settings live inside the opaque `properties_json` blob, so **this provider does not validate
them**. A data flow created without them saves and applies cleanly, but is unusable: the console
shows the required dropdowns empty and a run has no scope.

## 1. Recognise a connector that needs them

- Its `data_source_types` entry has `is_native = true` and
  `feature_flags.run_types = ["multi_tables"]`. (`is_native` is a literal field name on that
  catalog — read it, do not interpret it.)
- In `properties_json` its source is written as the literal `name = "native_connector"`, with
  `additional_settings.nc_id` / `additional_settings.nc_version` set. These are the exact strings the
  API accepts; the connector you actually configure is identified by `nc_id`, not by `name`.

The `boomi_data_integration_source_types` data source lists the available source types.

## 2. Discover which settings are mandatory

```
GET .../data_source_properties/global_properties?datasource_id=<slug>
```

Every descriptor in the response's `cross_reports_predefined[]` array with `"required": true` is
mandatory. For `github` that is **both** `organization` and `repositories`.

This endpoint is not published in the public OpenAPI document, but it is callable with the same
bearer token the provider uses.

## 3. Supply every required descriptor

Required settings go under
`source.additional_settings.interface_parameters.source`, as a list of
`{name, type, value}` objects:

```hcl
resource "boomi_data_integration_data_flow" "github_to_warehouse" {
  name = "github-prs"
  type = "source_to_target"

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "native_connector"
      run_type      = "multi_tables"
      connection_id = boomi_data_integration_connection.github.id
      additional_settings = {
        nc_id      = "<connector id from data_source_types>"
        nc_version = "<connector version from data_source_types>"
        interface_parameters = {
          source = [
            { name = "organization", type = "list_api_single_id", value = "<api-resolved id>" },
            { name = "repositories", type = "list_api_multiple_id", value = ["<api-resolved id>"] },
          ]
        }
      }
    }
    target = { name = "snowflake", connection_id = boomi_data_integration_connection.warehouse.id, loading_method = "overwrite" }
    schemas = [/* … */]
  })
}
```

Rules that bite:

- **`name` must equal the descriptor's `name` exactly.** A mismatch is saved without complaint and
  the console dropdown renders empty.
- **`value` format follows the descriptor `type`:**
  - `list_api_single_id` → one API-resolved id
  - `list_api_multiple_id` → a list of API-resolved ids — **not** raw display strings
  - `input_text` → the literal string

## 4. Verify

1. After apply, `GET` the data flow and confirm `interface_parameters.source` round-tripped.
2. Open the console's Source tab and confirm the values are **pre-selected**, not showing
   `Select…`.
3. Run a test connection on the source connection — the
   `boomi_data_integration_connection_test` data source does this from Terraform.

## Related

- [API connector data flows](./api-connector-data-flows.md) — the rest of the
  flow shape (`run_type`, `schemas`, `target`) these settings sit inside.
