# Logic data flow example (multi-step)

Creates a **logic data flow** (`type = logic` data flow) with the
`riveryio/data-integration` provider. A logic data flow runs an ordered pipeline of
steps; this example builds a `run_once` container with three step types:

1. **`river`** — run an existing data flow (e.g. the one from
   [`../source-to-target`](../source-to-target)) via its `cross_id`.
2. **`logicode` (Python)** — run a Python step *(optional — see the file_id note)*.
3. **`snowflake_sql_query`** — a SQL / DB transformation writing its `SELECT`
   result to a warehouse table.

The provider models the data flow body as opaque JSON (`properties_json`), so the
config emits the exact step shapes the API accepts:

```json
{
  "properties_type": "logic",
  "logic_steps": [{
    "type": "run_once", "name": "pipeline", "is_parallel": false, "steps": [
      { "type": "river", "river_id": "<cross_id>", "input_variables": {} },
      { "type": "logicode", "code_type": "python", "file_id": "<file_id>", "logicode_size": "XS", "additional_packages": [] },
      { "type": "snowflake_sql_query", "connection_id": "<conn>", "sql_query": "...",
        "target_settings": { "target_type": "table", "database_name": "...", "schema_name": "...", "table_name": "...", "loading_mode": "overwrite" } }
    ]
  }]
}
```

## Two run-time prerequisites

- **Python step needs an uploaded `file_id`.** The API cannot create code via
  Terraform. Leave `python_file_id = ""` (default) to omit the Python step; set
  it to a real `file_id` (from the UI code editor or another data flow) to include it.
- **Snowflake step needs `group_id`.** A logic data flow that uses a shared warehouse
  connection must set `group_id` to the environment's group cross_id, or the
  connection fails to route at run time with a misleading 404. This example sets
  it via `var.group_id` on the `boomi_data_integration_data_flow` resource.

## Run it

```bash
export DATA_INTEGRATION_API_URL=http://localhost:8008
export DATA_INTEGRATION_ACCOUNT_ID=<account_id>
export DATA_INTEGRATION_ENVIRONMENT_ID=<existing_env_id>
export DATA_INTEGRATION_API_TOKEN=<token>

terraform apply \
  -var "api_url=$DATA_INTEGRATION_API_URL" \
  -var "account_id=$DATA_INTEGRATION_ACCOUNT_ID" \
  -var "environment_id=$DATA_INTEGRATION_ENVIRONMENT_ID" \
  -var "group_id=<env_group_cross_id>" \
  -var "sub_data_flow_id=<cross_id of a data flow to orchestrate>" \
  -var "sf_account_name=<snowflake_account>" \
  -var "sf_username=<user>" -var "sf_password=<pass>" \
  -var "sf_target_database=<db>"
  # add -var "python_file_id=<file_id>" to include the Python step
```

Terraform creates the data flow **disabled**; activate + run it through the API
(`activate` + `run`) or the console. The number of pipeline steps is in the
`step_count` output (2 without a Python step, 3 with).
