# Documentation Accuracy Review — `riveryio/data-integration` Terraform Provider

**Date:** 2026-08-11
**Scope:** every file under `docs/` (33 files: `resources/`, `data-sources/`, `guides/`, `index.md`), cross-checked against three sources of truth:

1. The live OpenAPI spec — `https://api.rivery.io/openapi.json` (downloaded and queried directly with `python3`/`jq`, not summarized through a model — the spec is ~466KB / 65 paths and precision matters).
2. The live connection-type catalog endpoint — `GET {api_url}/v1/connections_types/{type}` (unauthenticated-scope-safe, returns the actual accepted `parameters_json` keys per connector).
3. Source code: this provider's own Go source (`internal/provider/`, `internal/client/`) and the API implementation, `rivery-api-service` (Bitbucket, internal).

Every finding below was independently verified by me against at least one primary source (not just repeated from a sub-agent pass) — see the reference link on each item. Provider repo links point at `main` (`856c330d1` at time of writing); `rivery-api-service` links point at `main` (`1b819e3b6`).

---

## High-impact: wrong field names in copy-pasteable examples

These are the first things a new user copies. All three ship a connection that silently drops or misassigns fields.

### 1. Snowflake `parameters_json` uses `account`/`database` instead of `account_name`/`default_database_name`

- **Where:** [`docs/resources/data_integration_connection.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/resources/data_integration_connection.md#L20) lines 20/23 (the page's *only* example), and [`docs/guides/connections.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/connections.md#L81) lines 81/83 ("Keyfile-backed credentials" example).
- **Doc claims:** `account = "xy12345.us-east-1"`, `database = "ANALYTICS"`.
- **Actual field names:** `account_name`, `default_database_name`. Full accepted set for `type = "snowflake"`: `warehouse, role, account_name, authentication_type, username, password, key_file_path, default_database_name, default_schema_name, json_indent`.
- **Verify yourself:** `curl -s {api_url}/v1/connections_types/snowflake -H "Authorization: Bearer $TOKEN" | python3 -m json.tool` — this is the exact live catalog the `boomi_data_integration_connection_type` data source itself reads from (see [`connection_type_data_source.go`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/internal/provider/connection_type_data_source.go)), so it's authoritative for what the API actually accepts.
- **Fix:** replace `account`/`database` with `account_name`/`default_database_name` in both examples. Since `guides/connections.md` explicitly tells readers *"guessing them from examples doesn't scale"* one paragraph before this exact example, the irony is worth noting — this is the one place the docs should be unimpeachable.

### 2. Snowflake key-pair upload field is `key_file_path`, not `private_key_file_path`

- **Where:** [`docs/guides/connections.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/connections.md#L87) line 87.
- **Doc claims:** `file_params = { private_key_file_path = "${path.module}/keys/snowflake_rsa_key.p8" }`.
- **Actual:** the field the API expects is `key_file_path`, type `file`, `file_type: "p8"`, gated on `authentication_type == "key_pair"`.
- **Verify yourself:** same live catalog call as above (the `key_file_path` entry has `"condition": {"field_name": "authentication_type", "equals": "key_pair"}`), corroborated by a fixture in `rivery-api-service`: [`tests/utils/endpoints_utils/test_connections_utils.py#L493`](https://bitbucket.org/boomii/rivery-api-service/src/1b819e3b6a7cf088d0382a3243375747e2b2536c/tests/utils/endpoints_utils/test_connections_utils.py#lines-493).
- **Fix:** `private_key_file_path` → `key_file_path`.

---

## High-impact: the "minimal" example is internally inconsistent, and duplicated in two files

- **Where:** [`docs/guides/getting-started.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/getting-started.md#L119) (Step 3, lines 119–140) and [`docs/guides/examples.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/examples.md#L160) (same block verbatim, lines ~160–175).

Four separate problems stacked in one ~20-line block:

| Line(s) | Doc has | Should be | Evidence |
|---|---|---|---|
| `getting-started.md:136` / `examples.md:172` | `run_type_and_datasource = "single_table"` | `"multi_tables"` or `"predefined_report"` — no other value is valid | `WriteSchemaInput.tables[].discriminator` in the OpenAPI spec (`components.schemas.WriteSchemaInput`) only maps `multi_tables → WriteDatabaseTableInput` and `predefined_report → BasePredefinedReportTableInput`. Fetch the spec and `grep -c run_type_and_datasource` yourself, or `jq '.components.schemas.WriteSchemaInput'`. |
| `getting-started.md:125` / `examples.md` equiv. | `additional_settings = { source_type = "source_to_target" }` | a connector-specific value (e.g. `"mysql"`, `"native_connector"`) | `"source_to_target"` is the river-level `properties_type`, not a valid `additional_source_settings.source_type` discriminator — looks like a copy-paste from the wrong field. |
| `getting-started.md:130-131` / `examples.md:166-167` | `schema = "PUBLIC"`, `db = "ANALYTICS"` on the target block | `schema_name`, `database_name` | `components.schemas.SnowflakeTargetSettings` in the spec has properties `database_name`/`schema_name` — no `schema`/`db` fields exist at all. Confirmed directly: `jq '.components.schemas.SnowflakeTargetSettings.properties | keys' openapi.json`. |
| (same block) | `loading_method` omitted entirely | required | `SnowflakeTargetSettings.required = ["loading_method"]` in the same schema — confirmed directly, not just documented elsewhere. |

- **Fix:** rewrite this shared example against the real `SnowflakeTargetSettings`/`WriteDatabaseTableInput` shapes — the provider's own [`docs/resources/data_integration_data_flow.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/resources/data_integration_data_flow.md) example already gets this right, so this is a matter of syncing the "getting started" copy with the resource page's own correct example rather than re-deriving anything.

---

## Invalid enum value: `table_status = "new_table"`

- **Where:** [`docs/guides/cdc-data-flows.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/cdc-data-flows.md#L29) line 29, and [`docs/guides/examples.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/examples.md#L292) line 292 (MongoDB CDC example).
- **Actual:** `TableStatusEnum` has exactly 4 members: `waiting_for_migration, migrating, tracked, live`. `"new_table"` does not exist anywhere in the codebase.
- **Verify yourself:** `python3 -c "from rivery_commons.enums.enums import TableStatusEnum; print(list(TableStatusEnum))"` inside an `rivery-api-service` venv (it's a third-party pip dependency, `rivery-commons`, not a file in either repo — that's why this one can't be a direct source link). Cross-referenced against usage in [`schemas/rivers/river_tables.py#L80-82`](https://bitbucket.org/boomii/rivery-api-service/src/1b819e3b6a7cf088d0382a3243375747e2b2536c/schemas/rivers/river_tables.py#lines-80:82), which uses `TableStatusEnum.WaitingForMigration` as its own example value for "snapshot before streaming" — likely the value both docs actually meant.
- **Fix:** `"new_table"` → `"waiting_for_migration"` in both places.

---

## `guides/metadata-and-schema.md`: one wrong attribute name, one conflated concept

- **Where:** [line 72](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/metadata-and-schema.md#L72) uses `source_type = "mysql"` as an argument to `boomi_data_integration_source_metadata`; actual attribute name is `datasource` — confirmed in [`internal/provider/source_metadata_data_source.go#L77`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/internal/provider/source_metadata_data_source.go#L77) (`Datasource types.String \`tfsdk:"datasource"\``). Note the sibling doc page [`data-sources/data_integration_source_metadata.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/data-sources/data_integration_source_metadata.md) itself already gets this right — it's just the guide that has it wrong.
- **Where:** [line 96](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/metadata-and-schema.md#L96) says `target_type = "snowflake"` lists databases and `"bq"` lists BigQuery datasets. Only `snowflake`, `bigquery`, `databricks` are valid values for this data source — confirmed by the provider's own error path: [`internal/provider/target_metadata_data_source.go#L145`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/internal/provider/target_metadata_data_source.go#L145), `"%q is not supported. Use one of: snowflake, bigquery, databricks."`. `"bq"` is a real string in the system, but it's the *internal* datasource id BigQuery uses in the unrelated `target_types` catalog (`TargetTypeInternalEnum.BIGQUERY = "bq"`) — the guide conflated two different "bq" meanings from two different data sources.
- **Fix:** `source_type` → `datasource`; drop the `"bq"` example or replace with `"bigquery"`.

---

## `guides/loading-methods.md`: `merge_method` table collapses 7 targets into one wrong row

- **Where:** [line 52](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/loading-methods.md#L52) — one table row claims BigQuery, Redshift, Databricks, Azure Synapse, Azure SQL, PostgreSQL, and Athena all accept only `insert_on_conflict`.
- **Actual**, per `rivery-api-service`'s [`schemas/rivers/river_additional_target_settings.py`](https://bitbucket.org/boomii/rivery-api-service/src/1b819e3b6a7cf088d0382a3243375747e2b2536c/schemas/rivers/river_additional_target_settings.py):
  - **Postgres** (`MergeMethodPostgres`): `{delete_insert, insert_on_conflict}` only.
  - **Redshift / BigQuery / Databricks / Azure SQL** (generic `MergeMethod`): all 4 — `{switch_tables, delete_insert, merge, insert_on_conflict}`.
  - **Athena and Azure Synapse Analytics:** no `merge_method` field exists at all for these targets.
- **Fix:** split the single row into per-target-family rows reflecting the actual 3 different value sets (the Snowflake row elsewhere in the same file is correct and can serve as the template for how granular this should be).

---

## `guides/api-connectors.md`: `is_native`/`feature_flags` don't exist on the data source being described

- **Where:** [lines 23–24](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/api-connectors.md#L23) tell readers to check `data_source_types` entries for `is_native = true` and `feature_flags.run_types = [...]`.
- **Actual:** the `boomi_data_integration_source_types` data source's schema has exactly 6 fields per entry — `id, name, connection_type, status, section_id, documentation_url` — confirmed directly in [`internal/provider/source_types_data_source.go#L26-31`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/internal/provider/source_types_data_source.go#L26). Neither `is_native` nor `feature_flags` is present anywhere in that struct. Following the guide as written produces an "attribute not found" error, not a warning.
- **Fix:** either add these fields to the data source (if the underlying API genuinely exposes them and it's a provider gap, not a doc gap), or remove the claim from the guide.

---

## Lower-confidence flags — worth a maintainer spot-check, not fully proven here

- **`fz_connection_id`** ([`resources/data_integration_connection.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/resources/data_integration_connection.md), [`guides/connections.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/connections.md#L109)): the Go provider sends this field on write ([`connection_resource.go#L244`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/internal/provider/connection_resource.go#L244)), but no code path in `rivery-api-service`'s connection-persistence logic (`utils/endpoints_utils/connections_utils.py`) appears to consume it by that exact name. It may be handled via Mongo-seeded per-connector-type schema data outside both repos — recommend a real `terraform apply` with this field set, then a read-back, before fully trusting the doc's description of its effect.
- **`aws_fz` connection-type slug** (`guides/connections.md`): doesn't appear as a literal string anywhere in `rivery-api-service`; the closest fixture ([`schemas/target_types.py:21-25`](https://bitbucket.org/boomii/rivery-api-service/src/1b819e3b6a7cf088d0382a3243375747e2b2536c/schemas/target_types.py#lines-21:25)) uses `connection_type = "aws"` for the same field set (`aws_access_key`, `aws_access_secret`, `region`). Worth confirming live whether `type = "aws_fz"` actually resolves, or whether it should be `type = "aws"`.
- **`data_integration_data_flow_group.md`** describes the data source as read-only. True of the *public* v1 OpenAPI spec (no `/river_groups` path there), but `rivery-api-service` has full CRUD on an internal, non-public-spec endpoint ([`api/api_v1/endpoints/river_groups.py`](https://bitbucket.org/boomii/rivery-api-service/src/1b819e3b6a7cf088d0382a3243375747e2b2536c/api/api_v1/endpoints/river_groups.py), registered in [`api/api_v1/api.py`](https://bitbucket.org/boomii/rivery-api-service/src/1b819e3b6a7cf088d0382a3243375747e2b2536c/api/api_v1/api.py)). Not necessarily a doc bug — may be a deliberate exclusion — but worth a one-line confirmation from whoever owns that decision.
- **`boomi_data_integration_connection_test`** ([`data-sources/data_integration_connection_test.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/data-sources/data_integration_connection_test.md)) is built on `POST /pull_requests`, which is explicitly commented `"internal endpoint for the UI"` in [`api/api_v1/endpoints/pull_requests.py`](https://bitbucket.org/boomii/rivery-api-service/src/1b819e3b6a7cf088d0382a3243375747e2b2536c/api/api_v1/endpoints/pull_requests.py) and is absent from the public OpenAPI spec entirely. Not a factual error in the doc — just an undisclosed dependency on a non-contractual internal endpoint that could change without a version bump. Worth a callout in the doc itself ("uses an internal API not covered by the public spec's stability guarantees").
- **`last_30_days`** appears as an example `time_period` value in both `getting-started.md` and `use-cases.md`. `RiverTimePeriodEnum` (13 real values) has no such member — but this sits inside a connector-specific free-form `additional_settings` blob, so it may be legitimate for a specific connector rather than universally wrong. Flagged, not confirmed.

---

## Verified accurate — checked, no issues found

`resources/data_integration_data_flow.md`, `_cdc_config.md`, `_run.md`, `_variables.md`, `_blueprint.md`, `_blueprint_file.md`, `_logicode_file.md`, `_dataframe.md`, `_environment.md`, `_variable.md`; all 8 `data-sources/*.md`; `guides/authentication.md` (matches `provider.go` almost verbatim, including exact error strings); `guides/activation.md`; `docs/index.md`; `guides/blueprint-data-flows.md`, `logic-data-flows.md`, `api-connector-data-flows.md`; `guides/data-flow-types.md`, `database-data-flows.md`, `incremental-extraction.md`, `environments-and-variables.md`; the rest of `cdc-data-flows.md` and `examples.md` beyond the specific lines flagged above.

## Coverage gaps — not verified, said explicitly rather than implied

- Per-connector parameter schemas beyond MySQL/Snowflake (Oracle, Jira, etc.) are seeded live in MongoDB, not present statically in the OpenAPI spec or `rivery-api-service` — use the live `GET /v1/connections_types/{type}` catalog endpoint per-connector rather than trusting any static doc, including the "verified accurate" list above for connector types not explicitly re-checked.
- The Go provider source was audited only for the specific claims above, not end-to-end for `properties_json`/schema-plumbing correctness.
- `guides/api-connectors.md`'s claim that GitHub's `organization`/`repositories` fields are both `required: true` needs live DB access to confirm and was not checked.

---

## Operational recommendation: always chain `connection_test` immediately after creating a connection

Not a doc-accuracy bug, but a real incident from this same review session worth folding into the docs: a `boomi_data_integration_connection` (type `mysql`) was created and its `source_to_target` data flow activated with zero errors from `terraform apply` — but every triggered run timed out after the platform's 10-minute watchdog (`RVR-TIMEOUT-400`). Root cause, found only after a full DB-level investigation: the host required a specific SSH-tunnel bastion (`rivery-ssh-tunnel.demo.rivery.in`) that every other connection to this same host in Rivery's history uses, which this config omitted. The host was directly reachable from a developer machine, so nothing about the connection *looked* wrong — it just wasn't reachable from the worker fleet's actual network path. `terraform plan`/`apply` had no way to catch this; only a full run-and-wait cycle surfaced it, and even then only as a generic timeout with no root cause attached.

The provider already ships the exact tool that would catch this immediately, at plan/apply time: `boomi_data_integration_connection_test` (see [`data-sources/data_integration_connection_test.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/data-sources/data_integration_connection_test.md)), which opens a real connection from the platform side and reports `success`/`error_message` immediately, with a `postcondition` block to fail the plan outright on a bad connection.

**Recommendation:** make the `connection` + `connection_test` pairing the *default* shown pattern everywhere a connection is created in the docs, not an opt-in advanced feature buried on its own data-source page. Concretely:
- Right now only `data_integration_connection_test.md` itself demonstrates this pairing. `guides/getting-started.md`, `guides/connections.md`, and every per-connector example on `resources/data_integration_connection.md` create a connection with no test attached at all.
- `getting-started.md`'s very first example is the highest-leverage place to add it — it's the first thing a new user copies, and the cost (one extra data source, a few seconds of plan time) is far lower than discovering a bad connection only after a river's first real run times out with a generic, uninformative error.

## Three more provider-behavior gaps found while implementing the SSH-tunnel fix

Found live, same session, while fixing the connection above. None are doc-accuracy bugs in the strict "this sentence is wrong" sense — they're undocumented provider *behaviors* that cost real debugging time and should either be called out in the docs or reconsidered in the provider itself.

### 1. A write-only-only attribute change can silently no-op

`parameters_json` and `file_params_content` are write-only ([`guides/connections.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/connections.md) covers the *why* — never stored in state), which means Terraform has no prior value to diff their new content against. In practice: updating an existing connection to add the SSH-tunnel fields (`is_ssh_tunnel`, `ssh_remote_host`, etc. inside `parameters_json`, plus a new `file_params_content` entry) while leaving every other attribute (`name`, `type`, `environment_id`) unchanged produced `terraform plan` → **"No changes. Your infrastructure matches the configuration."** — a silent no-op. The API was never called; the broken connection was untouched. Recovery required either bumping an unrelated ordinary attribute (e.g. appending a suffix to `name`) to force a real diff, or `terraform apply -replace=<address>`.

- **Verify yourself:** reproduce with any existing `boomi_data_integration_connection` — change only `parameters_json`/`file_params_content` content, run `terraform plan`, observe no diff is detected despite the JSON content actually differing.
- **Recommendation:** document this explicitly wherever write-only attributes are introduced (`guides/connections.md`'s "Keyfile-backed credentials" section is the natural place) — readers need to know that touching *only* a write-only field is not guaranteed to produce a plan diff, and what to do about it (bump an ordinary attribute, or `-replace`). This is a direct consequence of Terraform's write-only design (not a provider bug per se), but it's non-obvious enough that it deserves a callout rather than being left for users to discover via a confusing "no changes" on a config they know just changed.

### 2. `connection_test` chained to an existing (already-broken) connection makes `plan` a live, slow, blocking network call

The recommended pattern above (chain `boomi_data_integration_connection_test` right after `boomi_data_integration_connection`) works perfectly for a *new* connection — the test only runs post-apply once the connection exists. But once that pairing exists on an **already-created** resource being *modified* (the exact scenario when fixing a broken connection), `connection_id` is already known at plan time, so `terraform plan` itself evaluates the data source — meaning `plan` makes a real, live round trip to the platform's connection-test endpoint, against the *current* (still-broken) remote state, not the hypothetical post-apply state. In this session that meant `terraform plan` hung for the full `timeout_seconds` (default 180s) and then failed with a connection-test error, on a `plan` that should have been informational-only and instant.

- **Verify yourself:** create a connection + chained `connection_test` for a source with the wrong password; then edit the connection resource's `parameters_json` to fix the password (leaving the `connection_test` block as-is) and run `terraform plan` — the plan phase itself will block on the live test against the old, still-broken state.
- **Recommendation:** call this out explicitly in [`data-sources/data_integration_connection_test.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/data-sources/data_integration_connection_test.md) — plan-time evaluation of a data source that depends on an existing (not `create_before_destroy`-recreated) resource is a live external call, not a cheap check, and can reflect stale/broken remote state rather than the plan's hypothetical result. Suggest `terraform apply -target=<connection resource>` as the documented workaround for "I'm fixing a connection that already has a chained test."

### 3. `file_params_content_filenames` is required for SSH-tunnel key uploads too — not just "sometimes needed," and the resulting error is misleading

[`guides/connections.md`](https://github.com/RiveryIO/terraform-provider-data-integration/blob/main/docs/guides/connections.md) documents `file_params_content_filenames` in the context of Snowflake key-pair uploads, phrased as needed for entries "the API validates by extension" — reading as connector/field-specific rather than a blanket requirement. Omitting it for an SSH-tunnel private key upload (`file_params_content = { ssh_pkey_file_path = <pem> }` with no matching `file_params_content_filenames` entry) fails with:

```
API error 400: "File with extension ssh_pkey_file_path is not supported for connection type mysql"
```

This message reads as "mysql doesn't support this kind of file field" — a connector-capability error — when the actual cause is that the API fell back to treating the *field name itself* as the filename (since none was given) and rejected the resulting bogus "extension." The fix is simply adding `file_params_content_filenames = { ssh_pkey_file_path = "ssh_key.pem" }` alongside it.

- **Verify yourself:** reproduce with any `file_params_content` entry that has no corresponding `file_params_content_filenames` key — the 400 and its exact wording should reproduce for any connector, not just mysql SSH keys.
- **Recommendation:** two independent fixes worth making: (a) state plainly in `guides/connections.md` that `file_params_content_filenames` is required for *every* `file_params_content` entry, full stop, not scoped to "entries the API validates by extension" (every entry is validated by extension — that's the whole mechanism); (b) file this as an API-side error-message issue too, since `rivery-api-service` could easily detect "no filename provided, field name has no valid extension" and return a much clearer message than a fake unsupported-connector-type error.

## How to reproduce this review

```bash
# 1. OpenAPI spec — download once, query locally, never through a summarizing tool
curl -s https://api.rivery.io/openapi.json -o openapi.json
python3 -c "import json; d=json.load(open('openapi.json')); print(list(d['components']['schemas']['SnowflakeTargetSettings']['properties'].keys()))"

# 2. Live connection-type catalog (per-connector ground truth for parameters_json)
curl -s {api_url}/v1/connections_types/snowflake -H "Authorization: Bearer $TOKEN" | python3 -m json.tool

# 3. Source cross-reference
#    Provider:      ~/Documents/Dev/boomi-data-integration-terraform (github.com/RiveryIO/terraform-provider-data-integration)
#    API impl:       ~/Documents/Dev/rivery-api-service (bitbucket.org/boomii/rivery-api-service)
```
