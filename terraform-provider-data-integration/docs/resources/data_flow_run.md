# boomi_data_integration_data_flow_run (Resource)

Triggers a run of a data flow (river) on `apply` — the Terraform-native way to
execute the underlying API's `activate_river` + `run` actions.

Running a data flow is an **imperative action**, not persistent state. Terraform
models actions as first-class provider *Actions* only from Terraform **1.14+**;
for portability this capability ships as a resource instead. Creating the
resource fires exactly one run; change `triggers` (or otherwise force replacement)
to fire another. The resource does **not** poll or reconcile run status, and
destroying it does **not** cancel or undo a run.

## Example Usage

```hcl
# Run a freshly-created source-to-target river
resource "boomi_data_integration_data_flow" "load" {
  environment_id  = var.environment_id
  name            = "daily-load"
  type            = "source_to_target"
  properties_json = jsonencode({ /* source / target / schemas */ })
  # ...
}

resource "boomi_data_integration_data_flow_run" "load" {
  data_flow_id = boomi_data_integration_data_flow.load.id
  triggers = {
    ts = timestamp() # run on every apply
  }
}
```

## Schema

### Required

- `data_flow_id` (String) — cross_id of the data flow (river) to run. Changing it forces a new run.

### Optional

- `environment_id` (String) — Environment the data flow belongs to. Falls back to the provider-level `environment_id`. Changing it forces a new run.
- `activate` (Boolean) — Whether to activate (enable) the data flow before running it. Defaults to `true` (rivers are created disabled, so the first run needs activation). Changing it forces a new run.
- `triggers` (Map of String) — Arbitrary key/value pairs that, when changed, force a new run — the same pattern as `null_resource.triggers`. Use `{ ts = timestamp() }` to run on every apply, or a config hash to run when upstream config changes.

### Read-Only

- `id` (String) — Equals the triggered `run_id`.
- `run_id` (String) — The id of the run that was triggered.
- `run_group_id` (String) — The run group id returned for the triggered run.

## Notes

- If `activate = true` and activation is asynchronous, Create polls the returned
  operation to completion (up to 2 minutes) before triggering the run.
- The run itself is asynchronous: `apply` returns once the run is triggered, not
  when the data has finished loading. Observe completion via the console or the
  Activities API.
