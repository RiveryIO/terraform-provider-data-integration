---
page_title: "Test a connection (boomi_data_integration_connection_test)"
subcategory: "Connections"
description: |-
  Tests whether an existing connection can actually reach its source/target and authenticate, by having the platform open a real connection to it and read metadata (a get_db_metadata/get_schemas "pull request"). The API has no dedicated test-connection route; this reproduces what the console's "Test Connection" button does. The read does NOT fail when the connection is unreachable — instead success is false and error_message carries the real connector error (e.g. an ORA-* code). Assert on success in a lifecycle precondition/check block if you want a bad connection to fail the plan.
  task_type defaults to "source" — this tests whether the connection can be PULLED FROM, not whether it can be pushed to. For a data-warehouse connection used as a data flow's TARGET (Snowflake, BigQuery, Databricks), that default is the wrong check: the API rejects a warehouse tested as a source with a 400 "The connection does not match to the provided connection_type" — and because that is a hard error, not success = false, a postcondition on self.success never even runs; error_message is left empty and the actual failure surfaces as a provider error instead. Use boomi_data_integration_target_metadata to check a warehouse target instead — it issues the correct task_type = "target" request and doubles as reachability plus a live list of its databases/datasets/catalogs. If you do set task_type = "target" here directly, only the warehouse's own listing verb is accepted (e.g. get_databases for Snowflake); get_db_metadata is rejected with a 422 "did not match any key in the pull-translate mapping for this datasource_id".
  This data source only tests DATABASE connections. All three of its tasks (get_db_metadata, get_schemas, get_databases) are RDBMS verbs, so a SaaS/API connector (Jira, Shopify, Salesforce, …) rejects every one of them with the same 422 "did not match any key in the pull-translate mapping for this datasource_id"; the pull_requests route has no mapping for a SaaS datasource_id under any task name. The console CAN test these connections — its Test Connection button uses a different API surface — so this is a gap in what the v1 API exposes, not a missing capability. From Terraform, though, there is no pre-flight check for a SaaS connector (boomi_data_integration_source_metadata is RDBMS-only as well); budget a run to validate the credential. Note also that a passing Test Connection does not prove the credential can read data — a revoked Jira token has been observed passing the console test while its run finished succeeded with a warning and zero rows.
  This test is a live network call and it competes for platform workers. A test that completes in ~35s on its own can sit at operation status "R" past the 180s default when Terraform reads it concurrently with other live data sources, which it does by default. A timeout here is a hard provider error, not success = false, so a postcondition cannot catch it — raise timeout_seconds, run with -parallelism=1, or prefer boomi_data_integration_source_metadata, which proves the same connection AND returns the schema mapping the data flow needs, replacing this test rather than adding to it.
---

# Test a connection

Checks whether a connection can actually reach its source or target and authenticate — the same
check the console's **Test Connection** button runs.

## How it works

`boomi_data_integration_connection_test` opens a real connection using the credentials already stored on a
[`boomi_data_integration_connection`](../resources/data_integration_connection), and reads back its
metadata (tables, schemas, or the equivalent for that connector).

A connection that can't be reached does **not** fail the read: `success` comes back `false` and
`error_message` carries the real connector error (for example, an Oracle `ORA-*` code). If you want a
broken connection to fail the plan instead of quietly reporting `false`, assert on `success` yourself
with a `postcondition` — see the example below.

## Example Usage

```terraform
# Test a connection right after creating it, and fail the plan if it can't connect.
resource "boomi_data_integration_connection" "oracle" {
  environment_id = var.environment_id
  name           = "prod-oracle"
  type           = "oracle"
  parameters_json = jsonencode({
    database_architecture = "single-tenant"
    host                  = "db.example.com"
    port                  = 1521
    database              = "ORCL"
    database_service      = "service_name"
    username              = "app"
    password              = var.oracle_password
    is_ssh_tunnel         = false
  })
}

data "boomi_data_integration_connection_test" "oracle" {
  environment_id = var.environment_id
  connection_id  = boomi_data_integration_connection.oracle.id
  datasource_id  = "oracle"

  lifecycle {
    postcondition {
      condition     = self.success
      error_message = "Oracle connection failed: ${self.error_message}"
    }
  }
}
```

## Testing a warehouse TARGET: use `target_metadata`, not this data source

`task_type` defaults to `"source"` — this tests whether the connection can be **pulled from**, not
whether it can be **pushed to**. For a data-warehouse connection used as a data flow's target
(Snowflake, BigQuery, Databricks), that default is wrong: the API rejects a warehouse connection
tested as a source with `400 "The connection does not match to the provided connection_type"`.

That failure is a hard provider error, **not** `success = false` — so a `postcondition` on
`self.success` never even runs, `error_message` stays empty, and the misleading 400 is all you see.

Use [`boomi_data_integration_target_metadata`](./data_integration_target_metadata) to check a
warehouse target instead. It sends the correct `task_type = "target"` request and doubles as a
reachability check plus a live list of the warehouse's databases/datasets/catalogs.

If you set `task_type = "target"` here directly anyway, only the warehouse's own listing verb is
accepted (e.g. `get_databases` for Snowflake) — `task = "get_db_metadata"` (the default) is rejected
with a `422 "did not match any key in the pull-translate mapping for this datasource_id"`.

## This tests DATABASE connections only

All three of this data source's tasks — `get_db_metadata` (the default),
`get_schemas` and `get_databases` — are RDBMS verbs. Point it at a SaaS/API
connector (Jira, Shopify, Salesforce, …) and every one of them is rejected:

```
API error 422: {"detail":[{"type":"value_error","loc":["body"],
 "msg":"Value error, task: 'get_db_metadata' did not match any key in the
  pull-translate mapping for this datasource_id", …}]}
```

The `pull_requests` route this data source is built on has no mapping for a
SaaS `datasource_id` under any task name, so there is nothing to pass here
instead.

-> **The console can still test these connections.** A SaaS connection's
detail page has a working **Test Connection** button; it just runs through a
different API surface than the `pull_requests` route available here. This is a
gap in what the v1 API exposes, not a missing product capability — so do not
conclude a SaaS connection is untestable just because this data source cannot
test it.

What that leaves for Terraform: `boomi_data_integration_source_metadata` is
RDBMS-only too (see
[Schema & column mapping](../guides/metadata-and-schema)), so there is no
pre-flight check for a SaaS connector **from Terraform**. Budget a run to
validate the credential and read the run's `error_description`, rather than
expecting a bad credential to fail at `plan` or `apply`.

~> **A passing Test Connection is not proof the credential can read data.**
Observed on a Jira connection whose API token had been revoked: the console
reported *Test Connection Passed!*, the flow activated, and the run finished
`succeeded` **with a warning and zero rows** — `"No data retrieved from Jira.
Please check your dates criteria, filters or credentials."` The same token
returned `401` from Jira's own `/rest/api/3/myself`, while that site's
`/rest/api/3/serverInfo` answers `200` with no authentication at all. Treat
row counts in the target, not a green test, as the proof that a SaaS
credential works.

## This is a live network call, and it can run during `plan`

Reading this data source opens a real connection from the platform to your
source or target. It is not a cheap local check — budget for anything from a
few seconds to `timeout_seconds` (default 180).

**Concurrent reads make that much worse.** Terraform reads independent data
sources in parallel, and these tests compete for the same platform workers. A
test that finishes in ~35s on its own has been observed sitting at operation
status `"R"` past the 180s default when read alongside another live data
source. A timeout is a hard provider error, not `success = false`, so a
`postcondition` on `self.success` cannot catch it. Mitigations, best first:

- Prefer `boomi_data_integration_source_metadata` for an RDBMS source — it
  proves the same connection **and** returns the `schemas[]` mapping the data
  flow needs, so it replaces this test rather than adding a second round trip.
- Raise `timeout_seconds`.
- Run with `-parallelism=1`.

**When it runs matters.** For a connection Terraform is about to *create*, the
`connection_id` isn't known until apply, so the test is deferred to apply, as
you'd expect. But once the pairing exists on a connection that **already
exists** and is only being *modified*, `connection_id` is known at plan time —
so `terraform plan` evaluates the data source, against the connection's
**current** remote state rather than the state your plan would produce.

That is exactly backwards when you are mid-fix: repairing a broken connection
means every `plan` first blocks on testing the still-broken version of it, and
can fail on that before showing you the diff. Land the connection change on its
own first, then let the test run against the fixed connection:

```bash
terraform apply -target=boomi_data_integration_connection.mysql_source
terraform apply   # connection_test now evaluates the repaired connection
```

Worth keeping in mind for CI too — a `plan` on a pull request will make one
live connection attempt per `connection_test` in the configuration.

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `connection_id` (String) The connection (cross_id) to test.
- `datasource_id` (String) The source/target type slug of the connection (e.g. "oracle", "mysql", "postgres", "snowflake"). Used to build the typed pull-request.

### Optional

- `environment_id` (String) Environment ID. Falls back to the provider default.
- `inputs_json` (String) Optional extra pull_request_inputs fields as a JSON object, merged into the request (e.g. {"database_name":"MYDB"} for Snowflake, or {"schemas":["DEV"]}). connection_id and pull_request_type are always set by the provider.
- `task` (String) The pull-request operation that opens the connection. Defaults to "get_db_metadata". Other reachability tasks: "get_schemas", "get_databases".
- `task_type` (String) Whether the connection is used as a "source" (default) or "target". Get this wrong for a data-warehouse connection (Snowflake/BigQuery/Databricks) and the API returns a hard 400 error rather than `success = false` — see the data source description for why `boomi_data_integration_target_metadata` is the right tool for a warehouse target instead.
- `timeout_seconds` (Number) How long to wait for the test to finish before erroring. Default 180.

### Read-Only

- `error_message` (String) The connector error when success is false (empty on success).
- `id` (String) Equals operation_id — the id of the pull-request operation that ran the test.
- `operation_id` (String) The pull-request operation id.
- `run_id` (String) The run id of the test operation (useful for fetching logs).
- `status` (String) Terminal operation status: "D" (done/reachable) or "E" (error).
- `success` (Boolean) True when the connection was reachable and authenticated (status == "D").

## Related

- [Connections](../guides/connections) — setting up the connection this tests.
