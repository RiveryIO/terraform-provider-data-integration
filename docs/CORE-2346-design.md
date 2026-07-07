# CORE-2346 — Terraform Provider for Data Integration — Design Note

Canonical design note for the epic. Combines: the provider plan, the **verified** v1 API
coverage (route-table-accurate), the BDI-plugin client-pattern reuse, the validated POC,
state-backend design, e2e testing approach, and implementation steps.

Sources of truth: scan of `rivery-api-service` `/v1` router registry (`api/api_v1/api.py`)
+ route decorators in `api/api_v1/endpoints/*.py`; the BDI plugin's live-probed
`api-coverage.md` + `RiveryAPI` client; a scan of legacy `rivery_front`'s Angular API layer;
and an end-to-end POC run against the integration environment.

---

## 1. Executive summary / conclusions

- **Viable and de-risked.** `rivery-api-service` exposes a versioned `/v1` public API and
  auto-generates a filtered **public OpenAPI spec** (`utils/swagger_utils.py`,
  `IS_EXTERNAL_TAG`) → the provider's Go client can be **generated**, not hand-written.
- **MVP is fully unblocked on v1** — environment, connection (incl. key-pairs), variable,
  and river (CRUD + run/cancel/activate/copy/versions/restore/schedule) all have v1 routes.
- **The hard part is river-definition modeling, not plumbing** — read shape ≠ write shape,
  required `metadata`+`settings`, write-only secrets, pagination. All validated in the POC.
- **Auth is largely solved** — a plain Bearer token works today; the only open question is
  TTL/refresh for unattended `apply` in CI.
- **A handful of secondary capabilities are genuine v1 gaps** (river groups, deploy/package
  promotion, API-token management, connection-test, sub-river CRUD, full OAuth) — each needs
  a v1 endpoint before its provider resource can exist.

## 2. Verified v1 API coverage (vs legacy `rivery_front`)

The legacy UI mostly calls the **legacy `/api/` console API**; only 3 ops use `/v1` from the
UI. But the **new v1 API exposes far more** than the old UI consumes. The table below is
**route-table-accurate** (verified against `api/api_v1/api.py` + endpoint decorators), not
inferred from the frontend.

### Supported on v1 (✅) — provider resources need no API work
| Capability | Routes (verified) |
|---|---|
| **Rivers** | list, get, create, update, delete, copy, activate/disable, versions list/get, restore |
| **River run ops** | `run`, `cancel_run`, `run_scheduler` (`river_operations.py`) |
| **River schedule** | river sub-field (incl. retry config — CORE-2322) |
| **River variables** | get / put (`river_variables.py`) |
| **Connections** | GET, GET/{id}, POST, PUT/{id}, DELETE/{id} (`connections.py`) |
| **Connection key-pairs** | GET, GET/{id}, POST, DELETE/{id} (`key_pairs.py`) |
| **Connection / datasource types** | `connection_types`, `data_source_types`, `data_source_sections`, `data_source_properties` |
| **Environments** | GET, GET/{id}, POST, PUT/{id}, DELETE/{id}, totals (`environments.py`) |
| **Account/env variables** | GET, PUT, DELETE (`variables.py`) |
| **Users** | GET, GET/{id}, POST, PATCH/{id}, DELETE/{id} + invite/re-invite/attach-detach (`users.py`) |
| **Teams** | GET, GET/{id}, POST, PUT/{id}, PATCH/{id}, DELETE/{id} (`user_teams.py`) |
| **Runs/activities** | list runs, run logs, activities |
| **Other v1 routers** | `data_quality_tests`(+types), `recipes`(+files/metadata), `logicode`, `dataframes`, `audit_events`, `account_management`, `account_notifications`, `target_types`, `plans`, `dashboard`, `troubleshooting`, `river_search`, `marketplace_signup` |

### Genuine v1 gaps (❌ / ⚠️) — need API work before a provider resource
| Capability | Status | Evidence |
|---|---|---|
| **River groups / folders** | ❌ gap | no `group*/folder*` endpoint file |
| **Deploy / package promotion** | ❌ gap | no `package*/deploy*` file; `pull_requests` router is only `cancel_run`+`logs` |
| **API-token management** (list/create/revoke) | ❌ gap | `token.py` is only generate/exchange (GET generate, POST boomi-exchange) |
| **Connection test** | ❌ gap | no `/test` route in `connections.py` |
| **Sub-rivers CRUD** | ❌ gap | only `…/sub_rivers/{id}/run` exists |
| **OAuth connection setup** | ⚠️ partial | `oauth2.py` callback-only (`/oauth2_callback/{provider}`) |

## 3. Client patterns to reuse (from the BDI plugin `RiveryAPI`)

The BDI plugin (`boomi-bdi-copilot`) is a production customer-facing client for the same v1
API — port its load-bearing patterns into the provider's client layer:
- **Bearer-token auth**; region auto-detect by probing `/environments`; JWT `acc` claim
  decoded locally (no `/users/me`).
- **`edit` = read-modify-write**: GET current → deep-merge patch → PUT full body; lists
  (`schedulers`, `logic_steps`) are full-replace.
- **Strip server-only fields** before write: `title,id,cross_id,_id,account_id,group_name`. **Do NOT strip resource-specific name fields** (`environment_name`, `connection_name`) — the PUT endpoints for those resources expect those exact keys in the request body.
- **Normalize read shape**: list uses `river_cross_id`/`name`, detail uses `cross_id`/`name` → normalize to stable `id`/`title`. Normalization is READ-only. Each resource's Update patch must use the API's write-side key name (e.g. `environment_name` not `name`), because normalization is never reversed before write.
- **`or`-default pattern in API utils is a one-way street**: several API endpoints use `field = new_value or existing_value` (Python `or`), meaning sending `null` silently keeps the existing value — you cannot clear an optional field to null. Affects `description` on environments (and likely others). Provider tests for null→value→null cycles will fail against those endpoints until the API is fixed to use `if new_value is not None`.
- **Typed errors + retry**: 401/403/404/422/429/5xx mapping; 3× backoff on 5xx.
- **Attribution header**: `X-Boomi-Plugin: <name>/<ver> (account=…)` feeds the NR usage dashboard.

## 4. Validated POC (`boomii/boomi-data-integration-terraform`)

`riverctl` — a rivers-as-code reconcile loop (`plan`/`apply`/`destroy`/`list`) over the v1
API, with file-based state. **Validated end-to-end against `api.integration.rivery.in`**:
`list` (20 real flows) → create → idempotent no-op → description update (server-verified) →
destroy (404-confirmed).

Schema findings surfaced (direct provider inputs):
- **Read shape ≠ write shape** — GET returns `properties.logic_steps`; write wants it under a
  `properties_type`-discriminated object. `Read` must normalize or plans stay dirty.
- **Write requires `metadata` + `settings` objects** (inner fields default); `description`
  lives in `metadata.description` (top-level rejected). `logic_steps` ≥ 1.
- **List is paginated** (`items`/`next_page`).

Shipped: PR #1 (POC), PR #2 (CI: ruff + pytest + gated integration e2e), PR #3 (risk-based
auto-approve).

## 5. State backend (local → shared S3)

- **Provider phase (target):** use Terraform's native `backend "s3"` with `use_lockfile = true`
  (or DynamoDB lock) + bucket versioning + encryption. Per-env isolation via `key`. Don't
  hand-roll state.
- **Interim `riverctl`:** swap the local `.state/state.json` for an S3 object; wrap the
  reconcile in **acquire-lock → pull → reconcile → push → release-lock** (DynamoDB
  conditional-put lock). Read-only `plan`/`list` skip the lock. The lock is what prevents two
  concurrent `apply`s from clobbering the shared state.
- **Never** put state in git (concurrency + secret-leak). Git holds **desired** state
  (`rivers/*.json`) only.

## 6. E2E testing approach

- **Provider:** `TF_ACC` acceptance tests (`resource.Test`) — real CRUD + `destroy` per step
  against an integration account; **idempotency** (empty plan after apply); **import verify**
  (`ImportStateVerify`); **sweepers** for cleanup. Unit tests for the client/auth layer.
  - **Devbox target**: run acceptance tests against the local devbox API (`DATA_INTEGRATION_API_URL=http://localhost:8008`). The devbox API service runs on the same feature branch, so provider ↔ API contract mismatches surface before touching integration or production. Token: `uv run scripts/generate_token.py dev <account_id> <env_id>`.
  - **Orphaned state**: when a test fails mid-run, the API's soft-delete leaves resources in `is_deleting=True` state, which blocks re-creation (409 on the next run). Sweepers or unique names per run (timestamp suffix) are required to avoid this. Cleaning up requires either a retry-aware DELETE loop or a direct MongoDB update (`is_deleted=True, is_deleting=False`).
- **Module/config:** native `terraform test` (`*.tftest.hcl`) or Terratest; assert
  `terraform plan -detailed-exitcode` is clean after apply; ephemeral isolated env + unique
  names; always destroy in teardown.
- **Static gates on every PR:** `validate`, `fmt -check`, tflint, checkov/tfsec.
- **CI gating:** run integration e2e only when creds present (POC's pipeline already gates on
  `CLI_TOKEN`).

## 7. Implementation steps

1. **File v1 API-gap stories** against `rivery-api-service` for the resources you want from
   the ❌ list — prioritize **river groups/folders** and **deploy/package** if
   promotion-as-code is in scope.
2. **Scaffold** `terraform-provider-rivery` (Go + terraform-plugin-framework); generate the
   client from the public OpenAPI; wrap auth (bearer + refresh) + retry + error→diagnostic
   mapping (port BDI patterns).
3. **Implement MVP resources**: environment → connection (+ key_pair) → variable → river.
   Each: schema, CRUD, `ImportState`, docs, `TF_ACC` test. Handle read≠write normalization +
   write-only connection secrets.
4. **Add now-viable resources** (no API work): `rivery_user`, `rivery_team`, `rivery_key_pair`;
   candidate extras `rivery_data_quality_test`, `rivery_recipe`.
5. **State backend**: S3 + locking; per-env `key`.
6. **Acceptance + CI**: idempotency, import verify, sweepers; gate integration creds.
7. **Resolve auth open question**: token TTL/refresh for unattended `apply`.
8. **Add gap-dependent resources** as their v1 endpoints land (groups → deploy/package →
   token-management → connection-test → OAuth → sub-rivers).
9. **Publish**: tfplugindocs + GoReleaser signed builds → Terraform Registry (RiveryIO).

## 8. Open questions / risks

1. **Auth for unattended apply** — bearer token works; confirm TTL + refresh for CI. *(highest)*
2. **River schema complexity / drift** — full declarative modeling is the biggest eng risk;
   JSON-passthrough mitigates but weakens validation.
3. **Connection write-only secrets** — GET omits credentials; use TF `WriteOnly`, keep in
   state, never overwrite from `Read`.
4. **OpenAPI completeness** for the MVP resources (enough to generate the client?).
5. **Registry namespace + GPG signing** ownership.
6. **Provider ↔ API `/v1` contract policy** — versioning/compat between the two teams.
7. **Write-key mismatch risk for each new resource** — the API uses resource-specific field names in `PUT` bodies (`environment_name`, `connection_name`, etc.) that differ from the normalized read keys. For every new resource, explicitly verify the write-side field names match the API's input schema before implementing Update. The acceptance test for "update name in place" will catch this immediately.
8. **Optional field clearing** — any API endpoint that uses `field = new_value or existing` cannot have its optional fields cleared to null from the provider. Identify which fields on which resources have this limitation before designing the schema (Optional vs Optional+Computed).
