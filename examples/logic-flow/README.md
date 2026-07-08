# Logic river example

Creates a **logic river** (a `type = logic` data flow) with the
`boomi/data-integration` provider. A logic river orchestrates other rivers as an
ordered list of steps; this example runs a single **leaf step** that executes an
existing river referenced by its `cross_id` (`var.sub_river_id`) — for instance the
river built in [`../source-to-target`](../source-to-target).

The provider models the river body as opaque JSON (`properties_json`), so the
config emits the exact logic schema the API accepts:

```json
{
  "properties_type": "logic",
  "logic_steps": [
    { "type": "river", "name": "run-sub-river", "river_id": "<cross_id>", "input_variables": {} }
  ]
}
```

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
  -var "sub_river_id=<cross_id of a river to orchestrate>"
```

Terraform creates the logic river **disabled** (like every data flow in this
provider). Activate and run it through the API — the same `activate_river` +
`run` calls the `boomi_data_integration_data_flow_run` resource makes, or click
**Run** in the console.

## Notes

- Reuses an **existing** environment (`var.environment_id`); it does not create
  one, avoiding the per-environment token re-scoping needed for `environment`
  resources.
- Add more entries to `logic_steps` to build a multi-step pipeline (run several
  sub-rivers in order, pass `input_variables`, etc.).
