# Typed properties and OpenAPI codegen — design note

**Status:** design note only. Nothing in section 5 or 6 is implemented.
**Scope:** whether `properties_json` (and the other JSON escape hatches) on
`boomi_data_integration_data_flow` should become typed Terraform schema, and
whether that typed layer should be generated from the public OpenAPI spec.

**Evidence base and its limits.** Every spec claim below was checked against the
public spec document (`openapi 3.1.0`, `Boomi Data Integration API 1.0.0`, 64
paths, 325 component schemas, 24 `discriminator` objects). Counts were produced
by walking the parsed document, not by reading prose. Where a claim comes from
the **193-body write corpus** measured elsewhere, it is labelled as such — that
corpus was not re-measured here, so corpus figures are second-hand and are
flagged where they matter. The two sources disagree in a couple of places and
those disagreements are called out rather than smoothed over.

The typed `settings` / `schedule` blocks shipped alongside this note are the
*small* version of the question. This note is about the big one: `properties`.

---

## 1. What the spec actually models

### 1.1 The write body is a three-arm union whose source_to_target arm nests more unions

`POST` and `PUT .../rivers` both take `WriteRiverInput`. Required: `name`,
`type`, `metadata`, `settings`, `properties` — which is why the provider's
`buildBody` always sends `metadata` and `settings` objects even when empty.

`WriteRiverInput.properties` is a **3-arm** union, `discriminator.propertyName =
"properties_type"`:

| `properties_type` | schema | modelled fields |
|---|---|---|
| `source_to_target` | `WriteSourceToTargetRiverPropertiesInput` | `properties_type`, `source`, `target`, `schemas` |
| `logic` | `LogicProperties` | `properties_type`, `logic_steps` |
| `actions` | `ActionProperties` | `properties_type` — and nothing else |

Two things to notice immediately.

`RiverTypeEnum` has **four** values: `source_to_target`, `actions`, `logic`,
`connector_executor`. The `properties` discriminator mapping has **three** keys.
There is no `connector_executor` arm. The provider's `type` attribute documents
`connector_executor` as a valid data-flow type, so the spec's own union is
already narrower than the spec's own type enum.

`ActionProperties` declares `additionalProperties: false` and exactly one
property (the discriminator constant). A strict generator reading that produces
a struct that can carry no payload at all, and a strict validator built from it
rejects every real `actions` body. This is not a modelling subtlety — it is a
schema that is simply incomplete, and it is load-bearing for the conclusion in
section 2.

### 1.2 `target` — a 12-arm discriminated union (confirmed)

`WriteSourceToTargetRiverPropertiesInput.target`: `oneOf` with 12 `$ref` arms,
`discriminator.propertyName = "name"`. Full mapping, with the modelled field
count per arm:

| `name` | schema | fields |
|---|---|---|
| `athena` | `AthenaTargetSettings` | 17 |
| `azure_sql` | `AzureSqlTargetSettings` | 14 |
| `azure_synapse_analytics` | `AzureSynapseAnalyticsTargetSettings` | 15 |
| `bigquery` | `BigQueryTargetSettings` | 16 |
| `blob_storage` | `BlobStorageSettings` | 9 |
| `databricks` | `DatabricksTargetSettings` | 18 |
| `gcs` | `GcsTargetSettings` | 8 |
| `postgres_rds` | `PostgresTargetSettings` | 15 |
| `redshift` | `RedshiftTargetSettings` | 14 |
| `s3` | `S3TargetSettings` | 9 |
| `snowflake` | `SnowflakeTargetSettings` | 15 |
| `target_email` | `EmailTargetSettings` | 2 |

The 12 arms and the `name` discriminator are confirmed. **The "~15–18 fields per
arm" figure is not:** it holds for the seven warehouse arms (14–18) but not for
the file/email arms — `gcs` has 8, `s3` and `blob_storage` 9, and
`target_email` has **2**. The distribution matters for effort estimation, so the
honest number is "8–18, median 14.5, with one 2-field outlier", roughly 152
target fields in total.

Note also `additionalProperties: false` on
`WriteSourceToTargetRiverPropertiesInput` itself.

### 1.3 `SourceSettings.cdc_settings` — a 7-arm discriminated union (confirmed)

`anyOf[oneOf[7 arms], null]`, `discriminator.propertyName = "datasource_id"`:
`mysql`, `postgres`, `oracle`, `mongodb`, `mssql`, `boomi_for_sap`, `datahub`.
Confirmed exactly as described.

### 1.4 Per-table details are typed — `WriteDatabaseTableDetailsInput` (confirmed)

17 properties. The incremental machinery is fully typed:

- `extract_method` → `ExtractMethodEnum` = `all`, `incremental`, `log`,
  `change_tracking`, `system_versioning`. Confirmed, exactly those five, in that
  order. (This is the enum the `properties_json` description already warns
  about: `incremental`, never `increment`.)
- `incremental_field` → nullable string.
- `date_range` → `DateRange`, `running_number` → `RunningNumber`, `epoch` →
  `Epoch` — three typed, mutually exclusive incremental modes, each `anyOf[ref,
  null]`. The spec does not encode the mutual exclusion; it only types the three
  shapes. The "set exactly one" rule is server-side behaviour, not schema.
- plus `is_custom_incremental`, `cdc_settings`, `change_tracking_settings`,
  `system_versioning_settings`, `additional_source_settings`,
  `additional_target_settings`, `modified_columns`, `table_status`,
  `exporter_chunk_size`, `target_table`, `is_selected`, `name`.

`modified_columns` is itself a 12-arm union discriminated on `target_type`, and
`WriteSchemaInput.tables` is a 2-arm union discriminated on
`run_type_and_datasource` (`multi_tables` → `WriteDatabaseTableInput`,
`predefined_report` → `BasePredefinedReportTableInput`). Typing `schemas`
therefore means typing three nested unions, not one.

### 1.5 `SourceSettings.additional_settings` is free-form — with an important correction

`SourceSettings` has seven properties: `name`, `connection_id`,
`connection_name`, `run_type`, `cdc_settings`, `additional_settings`,
`custom_query_source_settings`.

`additional_settings` is:

```json
{"type": "object", "title": "Additional Settings", "default": {}, "…": "examples only"}
```

No `$ref`, no `oneOf`, no `discriminator`, no `properties`, no
`additionalProperties: false`. Confirmed: it is an untyped bag. The corpus
measurement of **183 distinct keys across 34 connectors** is consistent with
that, and this is the one field that genuinely earns a JSON escape hatch.

**Correction to the premise, though.** The claim "the 20
`*AdditionalSourceSettings` schemas are NOT wired into it" is true as stated —
they are not wired into `SourceSettings.additional_settings` — but they are *not
orphans*. All 20 are wired into a **different** field:
`WriteDatabaseTableDetailsInput.additional_source_settings`, a 20-arm union
discriminated on `source_type` (`mysql`, `postgres` → `PostgreSQLAdditional…`,
`blueprint` → `RecipeAdditional…`, `native_connector`, `snowflake`, …). So the
spec types per-**table** additional source settings and leaves per-**source**
additional settings free-form. Anyone reading "these 20 schemas are unused" and
deleting them from a generated client would break the table layer.

### 1.6 `settings` and `schedulers` — the spec is *stricter* than the corpus claim, and agrees

For the record, since the typed blocks shipped with this note depend on it:

- `RiverSettings` has exactly two properties, `run_timeout_seconds`
  (`integer|null`, "Null activates automatic timeout calculation mode") and
  `notification` (`NotificationSettings|null`). Matches the corpus 193/193.
- `NotificationSettings` has exactly three: `warning`, `failure`,
  `run_threshold`, each `RiverNotificationReport|null`. `RiverNotificationReport`
  = `{email (required), is_enabled (default false), execution_time_limit_seconds
  (integer|null)}`. Matches the corpus 193/193.
- `WriteRiverInput.schedulers` is `array of RiverSchedule` with **`maxItems: 1`,
  `minItems: 0`** and the description "Currently support only one scheduler".
  The corpus said "at most one element"; the spec says it *normatively*. The
  singular typed `schedule` block is therefore not a provider-side convenience —
  it is the spec's own cardinality.

One caveat: neither `RiverSettings` nor `RiverSchedule` sets
`additionalProperties: false`, so the spec does not forbid a third settings key
appearing later. That is the forward-compat cost of the typed blocks, and it is
why `settings_json` was deprecated rather than removed.

---

## 2. The governing constraint: the spec lags reality

Reported from the corpus (not independently verified here): a **working target
type `knowledge_hub`** appears in real river write bodies. What *is* verified
here: the string `knowledge_hub` **does not occur anywhere in the public spec** —
not in the 12-arm discriminator mapping, not as a schema, not in an example.

Combine that with `additionalProperties: false` on
`WriteSourceToTargetRiverPropertiesInput`, and the consequence is mechanical: a
strictly generated **closed** union rejects, at plan time, a target that works in
production today. The user's failure mode is "Terraform says my pipeline is
invalid; the platform disagrees" — the worst failure mode a desired-state tool
has, because there is no workaround inside the tool.

`knowledge_hub` is not an isolated case. Three independent instances of the same
lag are visible without leaving the spec:

1. `connector_executor` is in `RiverTypeEnum` but has no `properties` union arm.
2. `ActionProperties` is `additionalProperties: false` with a single
   discriminator field — no real actions body can validate against it.
3. `/recipes*` — the endpoints behind `boomi_data_integration_blueprint` and
   `boomi_data_integration_blueprint_file`, both of which are shipped, working
   resources in this provider — **do not exist in the spec at all** (0 of 64
   paths).

So the lag is not one missing enum value in one place. It is a structural
property of a curated external spec that trails an internally-evolving platform.

> **Central conclusion: any generated typed layer must keep an untyped
> fallback.** Concretely: for every union the generator closes, the provider must
> retain a path that accepts an arm the generator has never heard of. Not "should
> consider" — the alternative is a provider that becomes *less* capable than the
> API it wraps on the day the platform ships a connector, and stays that way
> until the spec, the generator, and a provider release all catch up. Today's
> `properties_json` has exactly one virtue, and it is this one.

The corollary for the typed blocks shipped with this note: `settings` and
`schedule` are safe to type *because* their unions are trivial (no arms at all —
they are plain objects with 2, 3, and 3 fields), the spec pins `schedulers` at
`maxItems: 1`, and the deprecated JSON attributes remain as the fallback. Typing
`target` is a categorically different bet.

---

## 3. What codegen cannot do

### 3.1 `POST .../pull_requests` is absent from the public spec

The provider's three discovery/validation features — `source_metadata` and
`target_metadata` data sources and the connection test — all issue
`POST .../environments/{env}/pull_requests`. In the spec:

- The only `pull_requests` path is
  `.../pull_requests/{pull_request_id}/cancel_run` (`post`). **The collection
  `POST` that creates a pull request is not in the document.**
- There is **no** `BasePullRequestSchema`. The only pull-request-ish component is
  `PullRequestStatus` — a status enum, not a request body.

A generator emits functions for the operations it is given. It would emit
`cancelPullRequestRun` and nothing else. Every discovery feature the provider has
would still be hand-written. (Independently established elsewhere: these routes
are internal-only because the curated external swagger filters on an
`IS_EXTERNAL_TAG`; the routes are live and callable, they are just not published.
Exposing them is reportedly a one-line tag change — but that is someone else's
repo and cannot be assumed.)

### 3.2 Even if it were present, codegen keys on `(path, method)`

The three operations share **one** URL and **one** method. They discriminate on
the *request body*:

| feature | `task_type` | `task` | `pull_request_inputs` |
|---|---|---|---|
| source metadata | `source` | e.g. `get_db_metadata` | source-shaped |
| target metadata | `target` | `get_databases` / `get_datasets` / `get_catalogs` | `{pull_request_type, connection_id}` |
| connection test | — | test-shaped | connection-shaped |

Standard OpenAPI generators key operations on `(path, method)` and, absent
per-operation schemas, produce **one** function taking **one** loosely-typed
body. Body-discriminated multiplexing over a single endpoint is a pattern
OpenAPI can barely express (`oneOf` on a request body with no discriminator) and
generators do not usefully consume. Three typed functions is not an achievable
output.

### 3.3 Codegen never produces the async orchestration

All three are **POST → poll**: the POST returns an operation/pull-request id;
the result arrives only after polling `GET .../operations/{operation_id}` to a
terminal status (`D` done, `E` error), on a 4-second poll interval, under a
caller-supplied timeout. The default in both metadata data sources is 3 minutes
and it is user-overridable via a `timeouts.read` block for a reason: BigQuery
`get_db_metadata` legitimately takes ~6 minutes over 151 datasets, so that
default is not sufficient for every source and callers must raise it. The same
POST → poll pattern governs `activate_river`, `disable_river`, and `enable_cdc`,
with 5-minute and 10-minute (CDC) deadlines in `data_flow_resource.go`.

`GET .../operations/{operation_id}` *is* in the spec. The loop that drives it is
not, and cannot be: retry cadence, terminal-status mapping, per-operation
timeouts, and the "PUT after POST to initialise the fire-service task entry
before activate" ordering quirk are all behavioural knowledge that lives in
`data_flow_resource.go` and `client.go`, not in any schema.

### 3.4 Therefore: an explicit two-layer split

| layer | contents | source |
|---|---|---|
| **Generated** | CRUD/write over the 64 published paths: rivers, connections, environments, dataframes, variables, logicode_file, `operations/{id}`, plus request/response types for `WriteRiverInput` and its reachable schema graph | `tfplugingen`/`oapi-codegen` from the spec, regenerated on spec bumps, never hand-edited |
| **Hand-written** | `pull_requests` (create + the three body shapes), the operation poller and its timeouts, `/recipes*` (blueprints), the activate/disable/enable-CDC ordering rules, and every untyped-fallback path | Owned, reviewed, tested by hand |

The boundary is not a matter of taste. Layer 1 is exactly "what the spec
publishes as `(path, method)` with a body schema"; layer 2 is exactly the
remainder, and the remainder is where all of the provider's hard-won behaviour
lives. Any codegen adoption that does not draw this line explicitly will end up
with generated code being hand-patched, which is the worst of both.

---

## 4. Migration path: `properties_json` → typed `properties`

The `settings` / `schedule` change shipped with this note is the template, and it
is deliberately the easy case. The same five-step shape applies to `properties`,
at roughly 30× the surface.

**Step 0 — coexistence mechanics (proven, this PR).**
Add the typed attribute beside the JSON one. Both Optional. Reject setting both
in `ValidateConfig` with an attribute-scoped error, so the conflict is a
plan-time failure rather than a silent precedence rule. Render whichever is set
into the same API body in `buildBody`. Keep the typed attribute *config-
authoritative* — never populate it from the API response — because the API
enriches what you send (steps gain `step_id`, settings gains a `notification`
block), so echoing the server value back both breaks Terraform's plan==apply
contract and produces a perpetual diff. Mark the JSON attribute
`DeprecationMessage`, do not remove it.

**Step 1 — slice one: `target` only.**
Recommended first slice. Justification:

- It is the union whose arms are enumerable *and* stable: 12 warehouse/file/email
  destinations that change on a quarterly, not weekly, cadence — unlike
  `additional_settings`' 183 keys across 34 connectors.
- It is the highest-value slice per field typed. `loading_method`,
  `database_name`, `schema_name` and their per-arm friends are the fields users
  most often get wrong, and the error today surfaces as a server-side 422 during
  apply rather than a plan-time error.
- It is self-contained: `target` does not nest another union (unlike `schemas`,
  which nests three).
- The `knowledge_hub` gap is a *design input*, not a blocker, precisely because
  it is discovered at slice one.

Concretely: `properties.target` becomes a single-nested attribute whose fields
are the **union of all 12 arms**, plus a required `name`, plus a
`settings_json`-style escape for unmodelled arms. Do **not** model 12 mutually
exclusive `target { snowflake { … } }` blocks — Terraform's schema has no
discriminated union, and 12 blocks with cross-block exclusivity validators is a
worse user experience than one flat block whose irrelevant fields are simply
unset. Per-arm validation (e.g. "`database_name` is required when
`name = "snowflake"`") belongs in `ValidateConfig`, where it can be advisory and
warn-only for unrecognised `name` values.

**Step 2 — slice two: `schemas[].tables[].details`.**
The incremental machinery: `extract_method` as an enum-validated attribute,
`incremental_field`, and typed `date_range` / `running_number` / `epoch` with a
"exactly one" validator the spec does not express. High value (this is where the
`incremental` vs `increment` footgun lives) but 3 nested unions deep, so it must
follow, not lead. `source_metadata`'s existing typed `date_range` output is a
head start and a consistency constraint — the two must agree.

**Step 3 — what does *not* get typed, ever.**
`source.additional_settings` stays a JSON blob. 183 keys / 34 connectors, no
schema, no union. Typing it would mean owning a moving target with no
specification, and would guarantee the section-2 failure mode on every new
connector.

**Deprecation staging.** Three provider minor releases minimum:
`N`: typed attribute added, JSON attribute deprecated, both work.
`N+1`: docs and examples converted; JSON path emits a warning on use (already
true via `DeprecationMessage`); telemetry-free, so measure adoption by asking.
`N+2` (major): remove the JSON attribute — *only* if the typed path has an
untyped fallback good enough that no user is stuck. If any user is still on the
JSON path because the typed path cannot express their config, that is a bug in
the typed path, not a stubborn user.

**What breaks, honestly.**
1. *State migration.* Moving a config from `properties_json` to `properties`
   changes which attributes are populated in state. Terraform sees the old
   attribute go null and the new one appear; with both Optional this plans as an
   update, not a replace — but it plans as a **diff**, and for `properties` that
   diff is the whole data flow. A real `terraform plan` on a converted config
   must be verified before this is recommended to anyone; the plan being empty is
   not guaranteed and is **not verified here**.
2. *`jsonencode` ergonomics lost.* Configs that build `properties_json`
   programmatically (`for` expressions over a table list, `jsondecode` of a
   `source_metadata` output) do so precisely because it is one value. A typed
   block cannot be assembled the same way; `dynamic` blocks help for lists but
   not for the discriminated `target`. Users doing this will resist the
   migration, correctly.
3. *`source_metadata` coupling.* `schemas_json` from that data source is decoded
   straight into `properties_json` today. Typing `schemas` requires the data
   source to grow a typed output too, or the coupling breaks.
4. *Doc density.* The current `properties_json` description is ~2 KB of
   hard-earned operational knowledge (native-connector `interface_parameters`,
   the `increment` footgun, `date_range` shape). That has to be redistributed
   across ~150 attribute descriptions without losing it. This is real work and
   is usually underestimated.

---

## 5. Verdict on adopting codegen

**Against — not yet. Type by hand, spec-guided, slice by slice.** Reasoning, in
the order that decided it:

**1. The generated layer would cover the easy part and miss the hard part.**
Of the endpoint surface the provider actually uses: `rivers`, `connections`,
`environments`, `dataframes`, `variables`, `logicode_file`, `operations/{id}` are
published — but `pull_requests` (3 features) and `/recipes*` (2 resources) are
not, and neither is any of the async orchestration. Codegen buys typed structs
for the calls that are already working and hand-written; it buys nothing for the
calls that are hard. The provider's difficulty was never "writing HTTP calls" —
it was learning that `metadata`/`settings` must be present, that description
lives at `metadata.description`, that PUT-after-POST initialises the fire-service
entry, that `postgres_rds` is the target name. None of that is in the spec, so
none of it is generated.

**2. The spec is not trustworthy enough to be the source of truth for
validation.** Section 2's list — `knowledge_hub` absent, `connector_executor`
armless, `ActionProperties` empty-and-closed, `/recipes*` missing entirely — is
four verified defects found in an afternoon of reading. A generated client that
merely *serialises* is tolerable; a generated client that *validates* against
this spec would reject working configurations. And the value proposition of
codegen is almost entirely the validation.

**3. Codegen is a coupling, and the coupled repo is not ours.** Regenerating on
spec bumps means an upstream change to a curated external swagger can alter this
provider's schema — and a Terraform provider schema is a public API with
compatibility obligations that the swagger's authors have not signed up for. A
field going from optional to required upstream becomes a breaking provider
release. Hand-written schema means upstream changes are a *pull* we review, not a
*push* we absorb.

**4. The cheaper path gets most of the benefit.** Use the spec as a **reference
and a test oracle**, not a generator:

- **Type by hand, slice by slice**, in the order of section 4, copying field
  names, enums, and nullability from the spec. That is what the `settings` /
  `schedule` work did: mirrored `RiverSettings` / `NotificationSettings` /
  `RiverSchedule` exactly, found the `maxItems: 1` that justified the singular
  block, and cost one focused change with full test coverage.
- **Add a spec-drift test.** Parse `openapi.json` in a Go test and assert the
  provider's typed attribute sets still match the corresponding schemas'
  properties (fail on a *missing* field, warn on an *extra* one). This is the
  single highest-value idea in this note: it catches the drift codegen is meant
  to prevent, at a fraction of the cost, and it fails in CI with a readable
  message instead of regenerating 20k lines. It also keeps working for the
  hand-written `pull_requests` layer, which codegen cannot touch.
- **Keep every union open.** Every typed block gets an untyped fallback, per
  section 2.

**5. The README's framing needs revising, not honouring.** The README lists a
"Generated client" as "the next structural step". On the evidence here that is
the wrong next step: the spec's coverage gaps (2 of the provider's resources and
3 of its data sources are unspecified) mean generation cannot own the client, and
its accuracy gaps mean it should not own validation. The genuinely next
structural step is **typed `properties.target` with an untyped fallback, plus a
spec-drift test** — which delivers the plan-time validation users want without
taking the coupling.

**What would change this verdict.** Any one of:

- `POST .../pull_requests` and `/recipes*` published, *and* the three
  body-discriminated operations split into distinct paths or given a
  `discriminator` on the request body. That removes objection 1 and shrinks
  layer 2 to just the poller.
- `knowledge_hub` (and whatever has landed since) present in the target
  discriminator, `connector_executor` given a `properties` arm, and
  `ActionProperties` actually populated — with a spec-freshness SLA behind it.
  That removes objection 2.
- The typed surface growing past roughly 150–200 attributes maintained by hand.
  At that scale the arithmetic flips and hand-maintenance becomes the more
  expensive option. Section 4's slice one (`target`, ~152 fields across 12 arms
  before de-duplication) is *right at* that boundary — which is the strongest
  argument for revisiting this decision immediately after slice one ships,
  measuring the actual cost, rather than deciding it in the abstract now.

**Uncertain / not verified in this note:** the corpus figures (193 bodies, 183
`additional_settings` keys across 34 connectors, `knowledge_hub` observed
working) are taken as reported; that `terraform plan` is empty across a
`properties_json` → `properties` conversion; that publishing the internal
`pull_requests` routes is genuinely a one-line change; and the concrete
generator's behaviour — objection 1 assumes a conventional
`(path, method)`-keyed generator (`oapi-codegen`, `openapi-generator`), and no
candidate generator was benchmarked against this document.
