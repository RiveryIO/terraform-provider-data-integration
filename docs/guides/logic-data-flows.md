---
page_title: "Logic data flows"
subcategory: "Logic"
description: |-
  The logic_steps contract for type = "logic" data flows: the step and container
  types the API accepts, logicode steps referencing a logicode_file, loops and
  conditions, and how the provider manages step_ids for you.
---

# Logic data flows

A **logic** data flow orchestrates work rather than moving one source into one target: it runs other
data flows, executes SQL against a warehouse, runs Python, and branches or loops over variables.

```hcl
resource "boomi_data_integration_data_flow" "pipeline" {
  name = "nightly-pipeline"
  type = "logic"

  properties_json = jsonencode({
    properties_type = "logic"
    logic_steps     = [/* … */]
  })
}
```

`type = "logic"`, `properties_type = "logic"`, and a **non-empty** `logic_steps` array. The API
requires at least one entry; this provider does not check that, so an empty array fails server-side
rather than at plan time. Nothing else in `logic_steps` is validated by the provider either —
`properties_json` is passed through verbatim.

## Step and container types

Every entry in `logic_steps` — and every entry in a container's nested `steps` — is discriminated on
`type`. The values the API accepts:

| Group | `type` values |
| --- | --- |
| Containers | `run_once`, `loop`, `condition` |
| Orchestration | `river` (run another data flow), `action` |
| Python | `logicode` |
| Snowflake | `snowflake_sql_query`, `snowflake_sql_script`, `snowflake_dataframe` |
| BigQuery | `bigquery_sql_query`, `bigquery_sql_script` |
| Redshift | `redshift_sql_query`, `redshift_sql_script`, `redshift_dataframe` |
| Databricks | `databricks_sql_query`, `databricks_sql_script` |
| PostgreSQL | `postgres_sql_query`, `postgres_sql_script` |
| Athena | `athena_sql_query`, `athena_sql_script` |
| Azure SQL | `azure_sql_query`, `azure_sql_script` |
| Azure Synapse | `azure_synapse_sql_query`, `azure_synapse_sql_script` |

Every step and container shares four fields: `name` (required), `is_enabled` (default `true`),
`disable_errors` (default `false`), and a server-assigned id (`step_id` on steps, `container_id` on
containers).

~> **Trap: the loop container's write-time `type` is `loop`, not `loop_over`.** The API also exposes a
read-only `container_type` field on run reporting whose enum spells it `loop_over`. That value is not
accepted as a step `type`.

## `river` — run another data flow

```hcl
{
  name            = "load-raw"
  type            = "river"
  river_id        = boomi_data_integration_data_flow.raw_load.id
  input_variables = {}
  is_enabled      = true
}
```

Required: `name`, `type`, `river_id`. `river_id` is the id of another data flow. (`river` is the
API's literal step type; it is the same object this provider calls a data flow.)

## `logicode` — a Python step

A `logicode` step executes a Python file. The API cannot accept code inline, so the code lives in a
separate [`boomi_data_integration_logicode_file`](../resources/data_integration_logicode_file)
resource and the step references it by id:

```hcl
resource "boomi_data_integration_logicode_file" "transform" {
  filename = "transform.py"
  content  = file("${path.module}/transform.py")
}

# … inside logic_steps:
{
  name                = "python-transform"
  type                = "logicode"
  code_type           = "python"
  file_id             = boomi_data_integration_logicode_file.transform.id
  logicode_size       = "XS"
  additional_packages = []
  is_enabled          = true
  disable_errors      = false
}
```

| Field | Notes |
| --- | --- |
| `file_id` | **Required.** The `logicode_file` resource's `id`. |
| `logicode_size` | **Required.** `XS`, `S`, `M`, `L`, `XL`, `XXL`. |
| `code_type` | `python` — the only value. |
| `additional_packages` | List of pip packages to install for the step. |

The logicode file API is create-and-read only: any change to `filename` or `content` forces a new file
and therefore a new `file_id`, which Terraform propagates into `properties_json` automatically.

## `run_once` — an ordered (or parallel) container

```hcl
{
  name        = "pipeline"
  type        = "run_once"
  is_parallel = false
  steps       = [local.load_step, local.transform_step]
}
```

Required: `name`, `type`, `steps` (at least one). `is_parallel` defaults to `false`, meaning the steps
run in array order; `true` runs them concurrently. `steps` accepts any step or container type,
including further containers.

## `loop` — iterate a multi-value variable

```hcl
{
  name                    = "per-tenant"
  type                    = "loop"
  loop_over_value         = "tenants"        # a multi-value data flow variable
  loop_over_variable_name = ["tenant"]       # bound fresh on each iteration
  steps                   = [/* … */]
}
```

Required: `name`, `type`, `steps`, `loop_over_value`, `loop_over_variable_name`.

`loop_over_value` names the multi-value variable being iterated —
declare it with `is_multi_value = true` on
[`boomi_data_integration_data_flow_variables`](../resources/data_integration_data_flow_variables).
`loop_over_variable_name` is a **list** of the variable name(s) bound per iteration; nested steps
reference them with `{tenant}` interpolation.

## `condition` — branch on a variable

A `condition` container is shaped differently from the others: its `steps` array does not hold steps.
Each entry is a **branch**:

| Field | Notes |
| --- | --- |
| `condition_name` | **Required.** Label for the branch. |
| `is_else` | **Required.** `true` marks the fallback branch, which carries `condition = null`. |
| `action` | **Required.** `run_step`, `skip_container`, `stop_river`, `fail_river`. |
| `condition` | `{ operator, operand_1, operand_2 }` — all three required when present, and all three plain strings. `null` on the `is_else` branch. |
| `step` | The step or container to run. Only meaningful with `action = "run_step"`; `null` otherwise. |

```hcl
{
  name = "guard-row-count"
  type = "condition"
  steps = [
    {
      condition_name = "no rows extracted"
      is_else        = false
      action         = "fail_river"
      condition      = { operator = "less_than", operand_1 = "{row_count}", operand_2 = "1" }
      step           = null
    },
    {
      condition_name = "otherwise transform"
      is_else        = true
      action         = "run_step"
      condition      = null
      step           = local.transform_step
    },
  ]
}
```

Branches are evaluated in array order. `run_step` runs the branch's `step`, `skip_container` abandons
the rest of the container, and `stop_river` / `fail_river` end the whole run. (`stop_river` and
`fail_river` are the literal API enum values; "river" is the API's word for the data flow itself.)

`operator` is a plain string in the API schema, not an enum, so it is not validated on write. Values
such as `equals` and `less_than` are attested by working configurations; the full set is not published
in the API document.

## SQL steps

A warehouse SQL step runs a query and writes the result somewhere. `snowflake_sql_query` requires
`name`, `type`, `connection_id`, `sql_query`, and `target_settings`. `target_settings` is a union
discriminated on `target_type`:

```hcl
{
  name          = "snowflake-transform"
  type          = "snowflake_sql_query"
  connection_id = boomi_data_integration_connection.snowflake.id
  sql_query     = "SELECT * FROM raw.orders WHERE status = 'open'"
  target_settings = {
    target_type   = "table" # table | variable | dataframe | files_export
    database_name = "ANALYTICS"
    schema_name   = "PUBLIC"
    table_name    = "open_orders"
    loading_mode  = "overwrite"
  }
}
```

For `target_type = "table"` the required fields are `target_type`, `table_name`, `database_name`,
`schema_name`, `loading_mode`. Note that a SQL step's field is `loading_mode`, whereas a
source-to-target flow's target uses `loading_method`.

~> A logic flow that uses a shared warehouse connection must set the data flow's `group_id` to the
environment's group, or the connection fails to route at run time with a misleading 404.

## `step_ids` are the provider's job, not yours

Do **not** write `step_id` into `properties_json`. The provider manages it:

1. On create it sends `logic_steps` with no `step_id` fields at all. The API mints them.
2. It reads them back and stores them, positionally, in the read-only `step_ids` list attribute.
3. On every update it re-injects the stored ids into `logic_steps` before the write, so existing steps
   keep their identity. Steps beyond the stored count get fresh ids from the API.

Two consequences worth knowing:

- `step_ids` tracks only the **top-level** `logic_steps` entries. Steps nested inside a container's
  `steps` array are not tracked there; the API assigns and keeps their ids.
- Re-injection is **positional**. Inserting, removing, or reordering a top-level step shifts the
  mapping, so the stored ids land on different steps. Appending to the end of `logic_steps` is safe;
  restructuring the middle of it is not.
