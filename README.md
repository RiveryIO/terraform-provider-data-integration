# boomi-data-integration-terraform

Infrastructure-/config-as-code for Boomi **Data Integration** (Rivery) data flows.

Tracks Jira epic **CORE-2346 — Terraform Provider for Data Integration**. This repo
currently holds a **validation POC** (`riverctl`, a "rivers-as-code" loop) that proves the
read-modify-write reconcile cycle against the Rivery public API end-to-end, plus the design
note comparing the approach to the full Terraform-provider plan. The Go provider itself is
the next phase (see Roadmap).

> Customer-facing term is **data flow**; the API/code term is **river**. Both refer to the
> same object — the API paths and fields use `river`/`cross_id`.

## What's here

```
riverctl.py                  CLI: plan / apply / destroy / list
rivery_client.py             slim Rivery public-API client (ports BDI plugin patterns)
rivers/*.json                desired state — one data flow per file (git-managed)
.state/state.json            actual state: name -> {cross_id, last_applied_hash}  (gitignored)
docs/CORE-2346-comparison.md design note: this POC vs. the full TF-provider plan
```

The loop:

```
rivers/*.json  (desired)  ──►  riverctl  ──►  Rivery public API  (actual)
                                  ▲
                          .state/state.json
```

`.state/` is the **state backend** stand-in (local temp folder). For a real deployment,
state should move to object storage (e.g. S3) with locking — **not** git (git state has
concurrency + secret-leak problems). Desired state (`rivers/`) is what belongs in git.

## Prerequisites

- [`uv`](https://docs.astral.sh/uv/) (the tool declares its deps inline via PEP 723 — no venv setup needed).
- A Rivery API token + account id + environment id for the target environment.

## Credentials (never committed)

`*.env` and `.state/` are gitignored. Provide credentials one of two ways:

**A. Point at an env file** (KEY=VALUE) with the api-service integration keys:

```bash
export RIVERY_IAC_ENV_FILE=/path/to/integration.local.env
# file must contain: CLI_TOKEN=...  API_URL=...  ACCOUNT_ID=...  ENVIRONMENT_ID=...
```

**B. Export the standard env vars directly:**

```bash
export DATA_INTEGRATION_API_TOKEN=...
export DATA_INTEGRATION_API_URL=https://api.integration.rivery.in
export DATA_INTEGRATION_ACCOUNT_ID=...
export DATA_INTEGRATION_ENVIRONMENT_ID=...
```

Optional: `RIVERY_IAC_STATE_DIR` to relocate the state file (default `./.state`).

## Usage

```bash
uv run riverctl.py list      # read-only smoke test — list rivers in the target account/env
uv run riverctl.py plan      # show CREATE / UPDATE / NO-OP / DESTROY for each rivers/*.json
uv run riverctl.py apply     # reconcile: create new, deep-merge-update changed; writes state
uv run riverctl.py destroy   # delete every river tracked in state; clears state
```

Typical flow:

```bash
export RIVERY_IAC_ENV_FILE=~/Documents/Dev/rivery-api-service/integration.local.env
uv run riverctl.py apply     # create the data flow(s) in rivers/
# edit rivers/poc_logic_flow.json (e.g. the description)
uv run riverctl.py plan      # -> ~ UPDATE
uv run riverctl.py apply     # applies the read-modify-write update
uv run riverctl.py destroy   # clean up
```

## Authoring a data flow

One JSON file per flow in `rivers/`. The `name` field is the logical key used in state.
Minimal **logic** flow (validated against integration):

```json
{
  "kind": "main_river",
  "type": "logic",
  "name": "my-flow",
  "metadata": { "description": "..." },
  "settings": {},
  "properties": {
    "properties_type": "logic",
    "logic_steps": [
      { "type": "river", "name": "step-1", "river_id": "<cross_id>", "input_variables": {} }
    ]
  }
}
```

### Schema gotchas (learned validating against the live API — CORE-2346 inputs)

- **Read shape ≠ write shape.** GET returns `properties.logic_steps`; CREATE/UPDATE expect
  `properties.logic_steps` under a `properties_type`-discriminated object. The client
  normalizes reads to stable `id`/`title`; the provider's `Read` must do the same or plans
  stay perpetually dirty.
- **`metadata` + `settings` objects are required on write** (their inner fields default).
  `description` lives in `metadata.description` — a **top-level** `description` is rejected
  (`extra_forbidden`).
- **`logic_steps` must have ≥ 1 item.** A leaf `river`-type step needs a real `river_id`.
- **Writes reject server-only fields** — the client strips
  `title,id,cross_id,_id,account_id,environment_name,group_name` before POST/PUT.
- **`edit` is read-modify-write** — GET current, deep-merge the patch, PUT the full body.
  Lists (`schedulers`, `logic_steps`) are full-replace, not index-merged.
- **List is paginated** — `{ items, next_page, total_items, ... }`, not a bare array.

## Validation status (integration)

Full loop exercised against `api.integration.rivery.in`: `list` → `apply`(create) →
`apply`(no-op) → edit → `apply`(update, server-verified) → `destroy`(404-confirmed). See
the CORE-2346 epic for the run log.

## Roadmap

1. **POC:** `riverctl` rivers-as-code loop + design note. ✅
2. **State backend:** move `.state/` to S3 (+locking); keep `rivers/` in git.
3. **Terraform provider — MVP:** `terraform-provider-data-integration/` (Go + terraform-plugin-framework),
   client ported from `rivery_client.py`; resources `boomi_environment` → `boomi_connection`
   → `boomi_data_flow` with full CRUD + `import`; unit tests + `TF_ACC` acceptance tests +
   examples + Registry docs. ✅ (`boomi_data_flow` verified live against integration; see
   `terraform-provider-data-integration/README.md`). See `docs/CORE-2346-comparison.md`.
4. **Provider — next:** generate the client from the public OpenAPI; add
   `rivery_dataframe` / `rivery_variable`; resolve auth TTL/refresh; GoReleaser → Registry publish.

**Open question carried from the plan:** customer auth for unattended `apply` — bearer-token
model exists today; confirm token TTL / refresh strategy for CI.

<!-- ci: trigger Harness pipeline boomidataintegrationterraform (CORE-2346) -->
