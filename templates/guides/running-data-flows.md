---
page_title: "Running data flows"
subcategory: "Operations & environments"
description: |-
  Triggering a run and polling it to completion from outside Terraform — endpoints, status values, and a reference script.
---

# Running data flows

This provider does not run data flows, deliberately. It is a desired-state
tool: it describes the connections, data flows and variables that should
exist, and reconciles them on every plan. Triggering a run is an *operation* —
there is nothing for `Read` to observe and nothing for `Delete` to undo — so
`boomi_data_integration_data_flow_run` is deprecated and should not be used in
new configurations.

Activation *is* desired state, and stays on the resource:

```hcl
resource "boomi_data_integration_data_flow" "orders" {
  # ...
  activate = true
}
```

That leaves two supported ways to actually run a flow.

## Option 1 — let the platform schedule it

The right answer for anything recurring. Define the schedule on the flow via
`schedule`, and the platform owns the timing:

```hcl
resource "boomi_data_integration_data_flow" "orders" {
  # ...
  activate = true

  schedule = {
    cron_expression = "0 */4 * * *" # every four hours
    is_enabled      = true
  }
}
```

Nothing else to operate — no CI job, no polling.

## Option 2 — call the run API from your pipeline

For a run at a specific point in someone else's workflow: after a `terraform
apply` in CI, downstream of an upstream job, or in an end-to-end test.

### The endpoints

All paths below are prefixed with
`{api_url}/v1/accounts/{account_id}/environments/{environment_id}` and take
`Authorization: Bearer {token}`.

| Purpose | Request |
| --- | --- |
| Trigger a run | `POST /rivers/{river_cross_id}/run` → **202** |
| Poll one run | `GET /rivers/{river_cross_id}/runs/{run_id}` |
| Fetch run logs | `GET /rivers/{river_cross_id}/runs/{run_id}/logs` |
| Cancel | `POST /rivers/{river_cross_id}/cancel_run` |

`river_cross_id` is the `id` attribute of `boomi_data_integration_data_flow` —
output it from Terraform and hand it to your pipeline.

The trigger returns a **run group**, because one flow can produce several runs
(sub-flows). For a single-flow run, the run you want is the first entry:

```json
{
  "run_group_id": "…",
  "river_cross_id": "…",
  "runs": [{ "run_id": "…" }]
}
```

### Status values

Polling correctly means knowing which statuses are final. There are only two
non-terminal values — anything else means stop:

| Status | Terminal? | Meaning |
| --- | --- | --- |
| `pending` | no | Queued, not yet picked up |
| `running` | no | Executing |
| `succeeded` | **yes** | Success |
| `failed` | **yes** | Failure |
| `canceled` | **yes** | Cancelled |
| `skipped` | **yes** | Did not execute |
| `partially succeeded` | **yes** | Run **groups** only — some child runs failed |

Treat any status you don't recognise as terminal-unknown and surface it rather
than looping; a poller that waits for `succeeded` and treats everything else as
"keep waiting" will hang for its full timeout on a `failed` run.

### Reference script

Standard library only, no dependencies. It exits non-zero on a failed run, so
it drops into CI as-is:

```python
#!/usr/bin/env python3
"""Trigger a data flow run and poll it to a terminal status."""
import json, os, sys, time, urllib.error, urllib.request

NON_TERMINAL = {"pending", "running"}
SUCCESS = {"succeeded", "partially succeeded"}


def _call(method, url, token):
    req = urllib.request.Request(
        url, method=method, headers={"Authorization": f"Bearer {token}"}
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {url} -> HTTP {e.code}: {e.read().decode()[:400]}")


def run_and_wait(api, account, env, river, token, timeout=1800, interval=10):
    base = f"{api}/v1/accounts/{account}/environments/{env}/rivers/{river}"

    started = _call("POST", f"{base}/run", token)
    run_id = started["runs"][0]["run_id"]
    print(f"run_id={run_id} run_group_id={started['run_group_id']}", flush=True)

    deadline = time.time() + timeout
    status = None
    while time.time() < deadline:
        status = _call("GET", f"{base}/runs/{run_id}", token).get("status")
        if status not in NON_TERMINAL:
            print(f"terminal status: {status}", flush=True)
            return run_id, status
        time.sleep(interval)

    sys.exit(f"run {run_id} still '{status}' after {timeout}s")


if __name__ == "__main__":
    _, status = run_and_wait(
        os.environ["BOOMI_API_URL"],
        os.environ["BOOMI_ACCOUNT_ID"],
        os.environ["BOOMI_ENVIRONMENT_ID"],
        os.environ["BOOMI_DATA_FLOW_ID"],
        os.environ["BOOMI_API_TOKEN"],
    )
    sys.exit(0 if status in SUCCESS else 1)
```

A runnable copy lives at
`examples/mysql-incremental-to-snowflake/run.py`.

Wiring it to Terraform's output:

```bash
# whichever output your configuration exposes the flow's id as
export BOOMI_DATA_FLOW_ID=$(terraform output -raw orders_data_flow_id)
export BOOMI_API_TOKEN=…   # same token the provider uses
python3 run.py
```

The token is read from the environment on purpose. Where it comes from — a
secrets manager, a CI secret, a vault — is environment-specific and outside
what this provider should prescribe.

### Verifying the data landed

A `succeeded` run means the platform reported success, not that the rows you
expected are in the target. For an end-to-end check, query the target directly
with its own client (`snowflake-connector-python`, `psycopg`, the `bq` CLI) and
assert on row counts. That check belongs in your pipeline, not in Terraform —
the provider has no visibility into target contents.

## Related

- [Activation](./activation.md) — what `activate` does and when it applies.
- [Connections](./connections.md) — testing a connection before you depend on it.
