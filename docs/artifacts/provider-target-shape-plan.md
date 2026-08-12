# Provider target shape — actions & structure

Plan for `terraform-provider-data-integration` (registry `riveryio/data-integration`).
Derived from reading the provider (~7.5k LOC Go), the Terragrunt client
(`data-integration-terraform-client`), and the 193-body example corpus in
`data-integration-sources-examples`.

---

## 1. Current surface (as built)

Framework `terraform-plugin-framework v1.19.0`, Go 1.25.8.
Three names by design: registry `riveryio/data-integration`, HCL local name `boomi`,
type prefix `boomi_data_integration_`.

**11 resources** — `environment`, `connection`, `data_flow`, `data_flow_run`,
`dataframe`, `logicode_file`, `variable`, `data_flow_variables`,
`data_flow_cdc_config`, `blueprint_file`, `blueprint`

**8 data sources** — `connection_types`, `connection_type`, `connection_test`,
`source_metadata`, `target_metadata`, `source_types`, `target_types`,
`data_flow_group`

**0 actions, 0 list resources, 0 ephemeral resources.**

Client is a single hand-written `internal/client/client.go` (1369 LOC), `map[string]any`
throughout, with read-modify-write deep-merge, forbidden-field stripping, null
stripping, id normalisation, typed errors, 5xx/429 retry, and operation polling.

---

## 2. Evidence base

Structural analysis of all 193 `post.json` bodies in the examples corpus
(41 sources × 7 targets; `custom_report` and `predefined` subtrees):

| Field | Presence | Verdict |
|---|---|---|
| `type` / `name` / `metadata` / `properties` / `settings` / `schedulers` | 193/193 | stable spine |
| `properties.properties_type` | 193/193 (`source_to_target`) | discriminator |
| `properties.source.{name,connection_id,run_type,cdc_settings,additional_settings}` | 193/193 | stable spine |
| `properties.target.{name,connection_id}` | 193/193 | stable spine |
| `settings.{run_timeout_seconds,notification}` | 193/193, exactly 2 keys | **fully enumerable** |
| `settings.notification.{warning,failure,run_threshold}` | 193/193, exactly 3 keys | **fully enumerable** |
| `schedulers[].{cron_expression,is_enabled}` | exactly 2 keys, ≤1 element | **fully enumerable** |
| `schemas[].tables[].details.{is_selected,target_table,extract_method,table_name,table_status}` | 1633/1633 table entries | stable spine |
| **`source.additional_settings`** | **183 distinct keys** | **not typeable** |
| **`properties.target.*`** | **31 distinct keys**, target-type-discriminated | **not typeable** |

`source.run_type` observed values: `predefined_report` (118), `regular` (66),
plus `entity`, `existing`, `new`, `feed`, `insights`, `insights_post`,
`insights_video`, `pages`.

---

## 3. Diagnosis — six structural problems

### P1. Imperative operations are modelled as CRUD resources and data sources
The wrong Terraform primitive is used for every side-effecting operation:

- `data_flow_run` is a resource whose `Create` fires a run, `Read` is a no-op,
  `Delete` is a no-op, and every attribute is `RequiresReplace` plus a
  `triggers` map. It is the `null_resource` pattern. The code says so itself:
  *"an imperative action modelled as a resource (Terraform provider Actions
  require Terraform >= 1.14)"* (`data_flow_run_resource.go:54`).
- `connection_test` is a **data source that opens real database connections**.
  Data sources read at plan time, so `terraform plan` dispatches worker jobs
  against production sources.
- `source_metadata` / `target_metadata` run 3–8 minute worker jobs at plan time.
  `examples/bq-source-schema-mapping` needs `timeouts { read = "8m" }` — an
  eight-minute plan.

### P2. `data_flow.activate` smuggles lifecycle orchestration into an attribute
`activate = true` makes `Create` do POST → PUT → activate (the PUT exists only to
initialise a fire-service task entry, or activation fails `RVR-ACTIVATE-500`), and
makes `Update` do disable → wait → update → [enable_cdc] → activate. That is a
five-step imperative saga hidden behind a bool.

### P3. Config-authoritative everywhere ⇒ no drift detection
`properties_json`, `settings_json`, `schedulers_json`, CDC `config_json`,
logicode `content`, blueprint `content`, and dataframe `connection_settings` are
all kept from config and never refreshed. Terraform's core value — detecting
out-of-band change — is absent for the majority of every data flow's definition.
Compounding it: the API's read-only `metadata` fields (`river_status`,
`is_deleted`, `last_activated`, `created_by`, `current_version_id`) are decoded
and then **discarded** — the provider surfaces no server state at all.

### P4. Two real gaps behind already-written client code
- `client.ListDataFlows` is referenced only from `client_test.go`. **No data
  source or list resource exposes it** — there is no way to enumerate existing
  data flows for discovery or import.
- `client.GetCDCConfig` is **genuinely unreferenced**: `cdc_config_resource.Read`
  is a deliberate no-op. Reading an offset is the primary operational use case
  and it is unreachable.

### P5. Duplicated async machinery
Five near-identical operation-poll loops: `data_flow_resource.waitForOpWithTimeout`,
`data_flow_run_resource.waitForOperation`, and three inline loops in the client
(`TestConnection`, `DiscoverSourceMetadata`, `DiscoverTargetMetadata`).

### P6. Stale surface / consumer drift
- `boomi_data_integration_river_variables` (with `river_id`) is used in **5 client
  units** but the provider registers `data_flow_variables` (with `data_flow_id`).
  Those five units cannot plan.
- `examples/bigquery-to-snowflake/cdc_variant.tf` documents "the provider has no
  scheduler attribute" as a verified gap; `schedulers_json` now exists, so the
  CDC data flow it commented out should work.
- The client's `modules/dataflow` precondition allows only
  `multi_tables|regular|predefined_report`, rejecting 8 of the corpus's real
  `run_type` values.
- `test_data_integration_terraform_client` and `data-integration-terraform-client`
  are two clones of the same remote; the former is behind and holds the stale
  `river_variables` usage.

---

## 4. Decision: Actions

### Hard constraint, verified in the framework source
`action.InvokeResponse` (framework v1.19.0, `action/invoke.go`) contains **only**
`Diagnostics` and `SendProgress`. **Actions cannot return values into the graph.**
They also gain `SendProgress(InvokeProgressEvent{Message})` — live progress
streaming, which none of the current long polls have.

This single fact partitions the work cleanly.

### Three approaches considered

**A. Move every imperative operation to Actions.**
Assumes discovery data sources can also be actions. **Falsified** — `source_metadata`
must return `schemas_json` into `properties_json`. Actions return nothing.
Likelihood: 0. Rejected.

**B. Keep everything as-is; document the sharp edges.**
Assumes practitioners tolerate plan-time DB connections and 8-minute plans.
Contradicted by the client repo, which had to wrap the provider in a 106-line
module with 6 preconditions and still gets `run_type` validation wrong.
Likelihood of being right long-term: low.

**C. Split by "does it return data?" — recommended.**
Side-effects → Actions. Reads → data sources. Assumption: the operations that
need to move return nothing but pass/fail. **Validated** against each one below.
Likelihood: high.

### The split

**Move to Actions** (side-effect only, pass/fail via diagnostics, `SendProgress`
for the poll):

| Action | Replaces | Note |
|---|---|---|
| `..._run_data_flow` | `data_flow_run` resource | a run is an event, not state |
| `..._activate_data_flow` | `data_flow.activate = true` | |
| `..._disable_data_flow` | implicit in `Update`/`Delete` | |
| `..._enable_cdc` | implicit in `Update` | one-time, currently invisible |
| `..._cancel_run` | *nothing — new* | `cancel_run` route exists, unexposed |
| `..._test_connection` | `connection_test` data source | **semantics change**, see below |

**Stay data sources** (must return values): `source_metadata`, `target_metadata`,
all four type catalogs, `data_flow_group`.

### The one genuine trade-off
`connection_test` today deliberately **does not fail** on an unreachable
connection — it sets `success = false` and `error_message`, so you assert in a
`precondition`. As an action it can only pass or fail via diagnostics; the
per-connector error text moves from an attribute into a diagnostic. That is
better semantics for a test gate but **is a breaking behaviour change**, and it
loses the "test and branch on the result" pattern. Recommendation: ship the
action *and* keep the data source (deprecated) for one minor, since only the data
source form can feed a conditional.

### Also worth adopting from v1.19
- **`list.ListResource`** → exposes the orphaned `ListDataFlows` for
  `terraform query`-driven discovery and bulk import (fixes half of P4).
- **`ephemeral`** → connection credentials. `file_params_content` already exists
  specifically to accept ephemeral values; an ephemeral resource completes it.

---

## 5. Decision: data flow schema

### Three approaches

**A. Status quo — one opaque `properties_json`.**
Assumes practitioners hand-author the blob. The corpus shows what that means:
183 distinct `additional_settings` keys. Correct as a *base layer*, insufficient
as the *only* layer.

**B. Fully typed schema per flow type.**
Assumes the schema is enumerable and stable. **Falsified** by the same 183 keys
and 31 target keys, both growing with every new connector. Rejected.

**C. Typed envelope + opaque leaves — recommended.**
Type the parts the corpus proves are 100% stable; keep JSON escape hatches for
the long tail. The corpus *is* the validation:

| Promote to typed | Corpus evidence |
|---|---|
| `settings_json` → `run_timeout_seconds` + `notification { warning/failure/run_threshold { email, is_enabled, execution_time_limit_seconds } }` | exactly 2 keys, 3 sub-keys, 193/193 |
| `schedulers_json` → `schedule { cron_expression, is_enabled }` block | exactly 2 keys, 193/193, max 1 element |
| `properties.source { name, connection_id, run_type }` | 193/193 |
| `properties.target { name, connection_id }` (+ optional `loading_method`, `database_name`, `schema_name`, `table_name`) | 193/193 core; `loading_method` 145/193 |
| `schemas[].tables[].details { name, target_table, extract_method, is_selected, table_status }` | 1633/1633 table entries |
| **Keep as JSON** | `source.additional_settings` (183 keys), `target.*` extras (31 keys), per-table extras |

`settings_json` and `schedulers_json` are the cheapest, highest-confidence wins:
both are fully enumerable, and typing them removes two `jsonencode()` calls from
every consumer while gaining real validation and drift detection.

Do **not** validate `run_type` with an allowlist at any layer — the corpus shows
8 connector-specific values beyond the obvious three. The provider is correctly
permissive today; the client module is not.

### Recover drift signal cheaply (fixes P3 without typing the blob)
Add computed, read-only attributes to `data_flow`, mapped from the `metadata`
the provider already decodes and throws away: `status` (`river_status`),
`is_deleted`, `last_activated`, `last_updated_at`, `current_version_id`.
This makes activation state and out-of-band deletion observable without touching
the config-authoritative blobs — the single biggest drift-detection gain per line
of code.

---

## 6. Target structure

```
internal/
  client/
    transport.go        request, retry, typed errors, headers        [extract]
    operations.go       pull_requests + the ONE operation poller     [dedupe P5]
    dataflow.go connection.go environment.go variable.go
    dataframe.go logicode.go blueprint.go cdc.go catalog.go
  provider/
    provider.go         Provider + WithActions + WithListResources
    resource_*.go
    datasource_*.go
    action_*.go                                                     [new]
    list_data_flow.go                                               [new]
    internal/jsonattr/  config-authoritative JSON helpers  [dedupe 4 copies]
    internal/opwait/    single operation-wait helper        [dedupe P5]
```

Keep the three-name scheme and the `boomi_data_integration_*` prefix — changing
it breaks every consumer and the docs pipeline (`tfplugindocs --provider-name boomi`).

---

## 7. Sequencing

**Phase 0 — fix what is broken now (no design risk)**
1. `river_variables` → `data_flow_variables` in the 5 client units.
2. Un-stale `cdc_variant.tf`; verify CDC create now works via `schedulers_json`.
3. Widen/remove the `run_type` precondition in `modules/dataflow`.
4. Reconcile the two client clones.
5. Wire `GetCDCConfig` into `cdc_config.Read`; expose `ListDataFlows`.

**Phase 1 — structure, no surface change**
6. Split the client; extract the single operation poller and JSON helpers.
7. Add computed `status`/`is_deleted`/`last_activated` to `data_flow`.

**Phase 2 — typed envelope (additive, non-breaking)**
8. Typed `settings` block alongside deprecated `settings_json`.
9. Typed `schedule` block alongside deprecated `schedulers_json`.

**Phase 3 — Actions (requires Terraform ≥ 1.14)**
10. `run_data_flow`, `activate_data_flow`, `disable_data_flow`, `enable_cdc`,
    `cancel_run`, `test_connection` — each with `SendProgress`.
11. Deprecate `data_flow_run`, `data_flow.activate`, `connection_test`;
    remove no earlier than the following major.

**Phase 4 — coverage**
12. `list` resource for data flows; `ephemeral` connection credentials.
13. Resources with existing v1 routes and no provider surface: `user`, `team`,
    `key_pair`, `data_quality_test`.

---

## 8. Open questions

1. **Terraform floor.** Declaring actions may raise the provider's minimum
   Terraform version for *all* users, not just action users. Needs verification
   against the protocol negotiation before Phase 3 — if it does, Phase 3 is a
   major bump.
2. **`data_flow_group`** stays read-only until the API drops
   `allow_from_api=false`. `group_id` therefore always references a pre-existing
   group. Confirm that is acceptable.
3. **Generated client.** The README targets generating from the public OpenAPI,
   but `pull_requests` / discovery routes are internal-only (not
   `IS_EXTERNAL_TAG`), so the discovery layer cannot be generated without
   exposing those routes. Decide: expose the tags, or keep a hand-written
   discovery client alongside a generated CRUD client.
4. **Token TTL/refresh** for unattended apply — still the open question carried
   from CORE-2346 §8.1.
