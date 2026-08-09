---
page_title: "Blueprint data flows"
subcategory: "Data flow types"
description: |-
  Using a blueprint as a data flow source: the blueprint_file / blueprint
  two-resource split, the source.additional_settings.recipe_id reference, and
  the per-table interface_parameters that feed a blueprint step.
---

# Blueprint data flows

A **blueprint** is a YAML definition of how to call an API — endpoints, pagination, response shape —
that a data flow can then use as its source. It is how you extract from an API that has no built-in
connector without writing any extraction code.

-> **Naming.** *Blueprint* is the customer-facing term. The API and its field names spell the same
concept `recipe` (`recipe_id`, `/recipes`). Where a literal API field appears below it is written as
the API expects it; everywhere else this guide says blueprint.

## Two resources, on purpose

The YAML content and the named thing that points at it are separate resources:

```hcl
resource "boomi_data_integration_blueprint_file" "posts" {
  filename = "posts.yaml"
  content  = file("${path.module}/posts.yaml")
}

resource "boomi_data_integration_blueprint" "posts" {
  name          = "jsonplaceholder-posts"
  description   = "JSONPlaceholder posts, managed by Terraform."
  file_cross_id = boomi_data_integration_blueprint_file.posts.id
}
```

| Resource | Holds | Key fields |
| --- | --- | --- |
| [`boomi_data_integration_blueprint_file`](../resources/data_integration_blueprint_file) | The YAML itself | `filename`, `content` (both required; `content` is sensitive and never read back from the API) |
| [`boomi_data_integration_blueprint`](../resources/data_integration_blueprint) | The named blueprint | `name` (required), `file_cross_id` (required — the file resource's `id`), `description` |

The split matters for two reasons:

- Editing the YAML updates the file **in place**. Both resources keep their ids, so nothing that
  references the blueprint has to change.
- Repointing `file_cross_id` swaps a blueprint onto different YAML without changing the blueprint's own
  id — again invisible to the data flows using it.

Destroying a blueprint does not delete the underlying file.

## Referencing the blueprint from a data flow

A blueprint-sourced flow is an ordinary `source_to_target` data flow. The blueprint is named on the
source:

```hcl
resource "boomi_data_integration_data_flow" "posts_to_snowflake" {
  name     = "jsonplaceholder-posts"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"

    source = {
      name     = "blueprint"
      run_type = "multi_tables"
      additional_settings = {
        source_type = "blueprint"
        recipe_id   = boomi_data_integration_blueprint.posts.id
      }
    }

    target = {
      name           = "snowflake"
      connection_id  = var.snowflake_connection_id
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
      loading_method = "overwrite"
    }

    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name         = "list_posts" # must match the blueprint's step name
          is_selected  = true
          target_table = "posts"
          additional_source_settings = {
            source_type = "blueprint"
            interface_parameters = {
              source = [
                { name = "user_id", type = "string", value = "1" },
              ]
            }
          }
        }
      }]
    }]
  })
}
```

| Field | Value |
| --- | --- |
| `type` / `properties_type` | `source_to_target`. There is no separate blueprint flow type. |
| `source.name` | The literal `blueprint`. |
| `source.run_type` | `multi_tables`. |
| `source.additional_settings.recipe_id` | The `boomi_data_integration_blueprint` resource's `id`. This is the field the API reads — hence the `recipe` spelling. |
| `source.additional_settings.source_type` | The literal `blueprint`. |

The `target` block is the same union as any other data flow — see
[Loading Methods](../guides/loading-methods#the-target-union).

## Per-step tables and `interface_parameters`

Each of the blueprint's steps becomes one entry under `schemas[].tables[]`, keyed by
`details.name`, which **must match the step's name in the YAML exactly**. A mismatch is saved without
complaint and the step is simply never invoked. Nothing in this provider or the API validates it —
`properties_json` is opaque to the provider, and the YAML is not parsed at write time.

Parameters the blueprint step expects are supplied per table under
`details.additional_source_settings`, using the `blueprint` arm of that union:

| Field | Notes |
| --- | --- |
| `source_type` | The literal `blueprint` — the union discriminator. |
| `interface_parameters` | The step's parameters, as `{ source = [{ name, type, value }] }`. |
| `filter_expression` | Optional row filter, e.g. `id > 100`. Defaults to `""`. |

Parameters shared by **every** step go on the source instead, in
`source.additional_settings.interface_parameters` — the API describes that field as carrying "the
global `interface_parameters` shared across all reports" for blueprint sources.

## Verification notes

The per-table `additional_source_settings` shape above is declared in the public API document (as the
`blueprint` arm of that union, with `source_type`, `interface_parameters` and `filter_expression`).

Three things are **not** published in the public API document: the literal `source.name = "blueprint"`
(absent from the documented source-name list), the `recipe_id` field (absent entirely), and the
`/recipes` endpoints the two resources call. All three are what the API actually accepts and are
exercised by working configurations, but treat them as undocumented-by-the-API rather than
schema-verified.
