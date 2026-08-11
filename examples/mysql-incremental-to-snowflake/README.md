# MySQL → Snowflake, incremental extraction

Backfill from a fixed start date, then track forward on an increment column.

This is the provider's **first incremental-extraction example**. Every other
example under `examples/` is a full reload (`extract_method: "all"`), so if you
have been copying a table mapping from a neighbouring example, you have been
copying a full reload. That is the gap this closes.

## Requires provider >= 1.1.0

`v1.1.0` is the first release whose `boomi_data_integration_source_metadata` data
source accepts `extract_method`, `incremental_field` and `date_range`. Before
that it hardcoded `extract_method: "all"`, so the documented discovery happy path
could only ever produce a full reload — no amount of configuration got you an
incremental flow through it.

## The one thing to get right: `incremental`, not `increment`

The API enum is `ExtractMethodEnum`:

```
all | incremental | log | change_tracking | system_versioning
```

**`incremental` is the canonical value. `increment` is not in the enum.**

You will nevertheless find `extract_method: increment` all over SaaS-source
examples and deployed SaaS flows. It is tolerated there for one reason only:
those tables are typed as `BasePredefinedReportTableDetails`, whose
`extract_method` is an **untyped free string** — nothing validates it. The RDBMS
path is different: a database table is `WriteDatabaseTableDetailsInput`, whose
`extract_method` is `ExtractMethodEnum` and **is enum-validated server-side**.

So for MySQL (and Postgres, SQL Server, Oracle, …) `incremental` is the only
correct spelling, and copying `increment` out of a Shopify or HubSpot flow is the
single easiest way to break this. The provider's `properties_json` documentation
says the same thing, and the `source_metadata` data source rejects invalid values
at plan time.

## `incremental_field` plus exactly ONE mode object

Incremental extraction needs two things beyond `extract_method`:

1. **`incremental_field`** — the source column that drives the increment. An
   update timestamp (`updated_at`, `modified_on`) or a monotonic id.
2. **Exactly one** of three *mutually exclusive* mode objects, matching the type
   of that column:

   | mode | use when the increment column is | key fields |
   |---|---|---|
   | `date_range` | a date / datetime | `time_period`, `start_date`, `end_date`, `days_back`, `split_time_intervals`, … |
   | `running_number` | an integer / numeric (e.g. auto-increment id) | `start_value`, `end_value`, `rows_in_chunk`, `include_end_value` |
   | `epoch` | a unix epoch number | `start_value`, `end_value`, `include_end_value` |

Set more than one and you are describing two contradictory windows. Also set
`is_custom_incremental = false` unless you really are using a custom increment
expression.

This example uses `date_range`. The data source only exposes `date_range`; the
other two modes are reachable through the hand-written route below.

### How "backfill from a date, then track forward" is expressed

```hcl
date_range = {
  time_period = "custom"                          # not year_to_date, not last_7_days
  start_date  = "2024-01-01T00:00:00.000+0000"    # where history begins
  # end_date deliberately UNSET / null            # open-ended upper bound
  days_back                    = 0
  include_end_value            = false
  split_time_intervals         = { time_interval = "days", interval_size = 7 }
  update_increment_on_failures = false
  utc_offset                   = 0
}
```

The three things doing the work:

- **`time_period = "custom"`** — the only `RiverTimePeriodEnum` value that honours
  an explicit `start_date`. The relative periods (`year_to_date`, `last_7_days`,
  `month_to_date`, …) compute their own window and ignore your date.
- **`start_date`** — the backfill floor. The first run pulls from here.
- **`end_date` left unset** — an open-ended upper bound is what makes this *track
  forward* rather than extract one fixed window. After each successful run the
  platform advances the increment marker, so the next run resumes where the last
  one stopped.

`split_time_intervals` chops a long backfill into chunks, so the first run does
not try to pull years of history in one request. It stops mattering once the flow
is caught up. `update_increment_on_failures = false` is the safe setting: a failed
run does not advance the marker, so nothing is silently skipped.

Enum values, straight from the spec — do not invent these:

- `RiverTimePeriodEnum`: `custom`, `yesterday`, `today`, `last_7_days`,
  `last_365_days`, `week_to_date`, `previous_week`, `previous_week_to_date`,
  `last_week`, `month_to_date`, `previous_month`, `previous_month_to_date`,
  `year_to_date`
- `IntervalTimeExternalEnum` (`split_time_intervals.time_interval`): `dont_split`,
  `minutes`, `hours`, `days`, `weeks`, `months`, `years`

## The two routes, and when to pick each

`main.tf` builds both, gated by variables. Defaults: route 1 on, route 2 off.

### Route 1 — discovery-driven (`create_discovery_driven_flow`, default `true`)

`boomi_data_integration_source_metadata` opens the live MySQL connection,
introspects the schema, and stamps `extract_method` / `incremental_field` /
`date_range` onto **every** discovered table. `schemas_json` then decodes straight
into the data flow's `properties_json`.

**Pick this when** your tables share one increment column and you want the column
list to come from the database rather than from a hand-maintained block that drifts
the moment someone adds a column.

**The catch:** the incremental settings are stamped on *identically*. Every
selected table must have a column named `var.incremental_field`. One table without
it and you need route 2.

**Second catch, worth knowing before you plan:** the data source emits
`modified_columns` without `is_key`, and `merge` de-duplicates on key columns. So
route 1 in this example maps over the decoded schemas and stamps `is_key` on from
`var.merge_keys`. With `loading_method = "append"` that transform is unnecessary
and you can feed `jsondecode(...schemas_json)` in unmodified, exactly as the data
source's own doc example shows.

### Route 2 — hand-written schemas (`create_hand_written_flow`, default `false`)

The same mapping written out literally in `properties_json`.

**Pick this when** you need per-table control:

- **a different increment column per table** — the main reason. The example uses
  `updated_at` on `customers` and `modified_on` on `orders`.
- **`modified_columns` deltas.** Source columns are all selected by default;
  `modified_columns` records only the *differences* — deselect a column
  (`is_selected = false`), rename it in the target (`alias`), mark a merge key
  (`is_key = true`). Route 1, by contrast, emits every column explicitly.
- **per-table `exporter_chunk_size`**, `table_status`, or a different
  `date_range` window per table.
- **`running_number` or `epoch`** instead of `date_range`.

Per-table fields available under `details` (from `WriteDatabaseTableDetailsInput`;
`name` is the only required one): `name`, `target_table`, `is_selected`,
`extract_method`, `incremental_field`, `is_custom_incremental`,
`exporter_chunk_size`, `table_status`, `modified_columns`, and one of
`date_range` / `running_number` / `epoch`.

## Why `loading_method = "merge"`

Deliberate choice, not a default.

The increment column here is an **update** timestamp. A row that changes gets a
new `updated_at` and is therefore re-extracted on a later run — the same business
row arriving twice. `merge` upserts it on the table's key columns, so the
warehouse keeps one current version per key. `append` would stack the old and new
versions side by side and every downstream `count(*)` would be wrong until
someone added a de-duplicating view.

`merge_method` is only meaningful when `loading_method` is `merge`. The API
defaults it to `merge`, but this example sets it explicitly so the config decides
rather than a server-side default. Valid values for a Snowflake target
(`MergeMethodSnowflake`): `merge`, `delete_insert`, `switch_tables`. Note the
unrestricted `MergeMethod` enum also lists `insert_on_conflict` — Snowflake does
not accept it; that one is for Postgres.

**When `append` is right instead:** an insert-only table with a monotonic increment
column — an append-only event/audit log keyed on `created_at` or an auto-increment
`id`. Rows are never updated, so nothing can arrive twice, and `append` skips the
merge cost entirely. In that case also drop the `is_key` stamping from route 1.
`overwrite` is the wrong answer for any incremental flow: it truncates the target
each run, which throws away exactly the history the increment is accumulating.

## Scheduling and activation

- **`schedulers_json`** is what this example uses, because it is what the released
  `v1.1.0` has. A **typed `schedule` block** (`cron_expression` + `is_enabled`)
  has landed on this repo's `main` and deprecates `schedulers_json`, but it is not
  in `v1.1.0` — switch once the next version ships. Same story for the typed
  `settings` block vs `settings_json`. Exactly one scheduler is allowed, and the
  cron must fire between once per day and 12 times per hour.
- **`activate`** is passed explicitly. On the released `v1.1.0` it defaults to
  `false`. On `main` the default has been **removed**: omitting it means
  activation is unmanaged (the provider adopts whatever the server reports and
  never activates or disables), while an explicit `true` / `false` is an enforced
  desired state that shows console-side changes as drift. Passing a value
  explicitly behaves identically on both, which is why this example always does.
- Both `activate` and `schedule_enabled` default to `false`, so a copy-paste
  apply authors the flow without pulling any data.

## Running it

```bash
cp terraform.tfvars.example terraform.tfvars   # git-ignored; fill it in
export DATA_INTEGRATION_API_TOKEN=<token>      # better than putting it in tfvars

terraform init
terraform plan
```

Both connections have a `boomi_data_integration_connection_test` attached with a
`postcondition`, so `apply` fails immediately and by name if either one can't be
reached — rather than applying cleanly and leaving you to discover it minutes
later when the first run dies on a connect timeout.

If the MySQL test fails against a host you *can* reach from your own machine,
read the SSH-tunnel section of the Connections guide before changing anything
else: runs execute on the platform's worker fleet, which egresses from different
addresses than your laptop, so local reachability proves nothing.

The `discovered_incremental_mapping` output exists so you can eyeball what
discovery produced — the exact `schemas[]` block, with merge keys stamped on —
before you trust it. Diff it against `local.hand_written_schemas` to see how the
two routes differ.

### Triggering a run

Terraform creates and activates; it does not run. `run.py` here triggers a run
and polls it to a terminal status — standard library only, exits non-zero on a
failed run so it works in CI unchanged:

```bash
export BOOMI_API_URL=https://api.rivery.io
export BOOMI_ACCOUNT_ID=<account id>
export BOOMI_ENVIRONMENT_ID=<environment id>
export BOOMI_DATA_FLOW_ID=$(terraform output -raw discovery_driven_data_flow_id)
export BOOMI_API_TOKEN=<token>

python3 run.py
```

A `succeeded` run means the platform reported success, not that the rows you
expected are in Snowflake. For a real end-to-end check, query the target
directly and assert on row counts. See the "Running data flows" guide for the
endpoints and the full status table.

Credentials are variables only — `mysql_password` and `snowflake_password` are
marked `sensitive`, and `parameters_json` is write-only, so neither reaches state.
For Snowflake key-pair auth, drop `password` and upload the `.p8` via the
connection resource's `file_params_content` (keeps the key off local disk *and* out
of state).

## Verification status — read this before trusting the `date_range` sub-shape

Be clear about what is and is not proven here.

**Verified:**

- `terraform init` + `terraform validate` pass against the released registry
  provider `riveryio/data-integration v1.1.0`. Every attribute used exists in that
  released schema.
- The enum values quoted above are read directly out of the public
  `Boomi Data Integration API` OpenAPI 3.1.0 spec (`ExtractMethodEnum`,
  `RiverTimePeriodEnum`, `IntervalTimeExternalEnum`, `MergeMethodSnowflake`,
  `LoadingMode`).
- The rendered `properties.schemas[]` payload from route 2 validates against the
  spec's `WriteSchemaInput` / `WriteDatabaseTableDetailsInput` / `DateRange` /
  `SnowflakeModifiedColumn` schemas.

**NOT verified — treat the `date_range` sub-shape as unproven for RDBMS:**

The `date_range` field layout used here comes from two sources: the OpenAPI spec's
`DateRange` schema, and a real deployed **Shopify** flow. Shopify is a SaaS source,
so its tables go down the untyped `BasePredefinedReportTableDetails` path — a
different code path from an RDBMS table's enum-validated
`WriteDatabaseTableDetailsInput`.

**Whether the RDBMS path accepts the identical `date_range` sub-shape has not been
confirmed against a live MySQL source.** Nothing in this directory has been
applied against any environment; no data flow was created. The spec says the two
share the `DateRange` schema and the shapes line up, but "the spec says so" and
"a live MySQL flow backfilled and then tracked forward" are different claims, and
only the first one is made here. If you are the first to apply this against a real
MySQL source, please update this section with what actually happened.

One related spec quirk found while checking: `modified_columns` is a
discriminated union over twelve per-target column schemas keyed on `target_type`,
which the spec marks *"Internal field, populated automatically"* and gives a
default. This example omits it, matching what the `source_metadata` data source
emits on the proven path — with the consequence that a strict `oneOf` validator
cannot pick a branch and reports the column as ambiguous (all twelve match).
Supplying `target_type: "snowflake"` resolves it to exactly one. Omitted here
rather than diverging from the shipped, working output.
