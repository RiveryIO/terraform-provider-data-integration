---
page_title: "CDC data flows"
subcategory: "Data flows"
description: |-
  Log-based (CDC) data flows: the mandatory scheduler and its cron bounds, how
  the provider enables CDC on activation, and how to seed or reset the source
  offset.
---

# CDC data flows

A **CDC** (log-based) data flow reads its source's change log instead of querying tables. It is
selected per table with `extract_method = "log"` inside `properties_json` — see
[Incremental extraction](../guides/incremental-extraction) for where that field lives.

CDC flows have two rules that non-CDC flows do not.

## Snapshot vs. stream-only

Each table in a CDC flow has two fields that control whether it starts with a
full-table snapshot or streams from the current log position immediately.

### `migrate_then_stream` — start with a snapshot

Use when the target table is empty and you need historical data loaded first:

```hcl
details = {
  table_status = "new_table"
  cdc_settings = {
    initiate_table               = true   # run snapshot before streaming
    overwrite_table_in_migration = false
  }
  # ...
}
```

### `stream_only` — no snapshot

Use when the target is already populated and you only need changes going forward:

```hcl
details = {
  table_status = "tracked"
  cdc_settings = {
    initiate_table               = false  # skip snapshot
    overwrite_table_in_migration = false
  }
  # ...
}
```

See [`examples/cdc/`](../../examples/cdc/) for both variants in a single runnable configuration.

---

## A scheduler is mandatory

The API **refuses to create or enable a CDC data flow that has no enabled scheduler**:

```
Please schedule a CDC data flow before enabling or creating
```

So a CDC flow must set the typed `schedule` block (or the deprecated `schedulers_json`) with
`is_enabled = true`:

```hcl
resource "boomi_data_integration_data_flow" "cdc" {
  name     = "mysql-cdc"
  type     = "source_to_target"
  activate = true

  schedule = {
    cron_expression = "*/15 * * * *"
    is_enabled      = true
  }

  properties_json = jsonencode({ /* … extract_method = "log" per table … */ })
}
```

### Cron bounds

The cron expression is a 5-field UNIX cron, and the schedule must fire **between once per day and 12
times per hour** — i.e. an interval between 5 minutes and 24 hours. Anything outside that band is
rejected for a CDC flow.

| Cron | Interval | Accepted |
| --- | --- | --- |
| `*/5 * * * *` | 5 minutes (12×/hour) | yes — fastest allowed |
| `0 * * * *` | 1 hour | yes |
| `0 3 * * *` | 24 hours | yes — slowest allowed |
| `*/1 * * * *` | 1 minute | no — faster than 12×/hour |
| `0 3 * * 0` | 1 week | no — slower than once per day |

Exactly one scheduler is allowed. The typed `schedule` block is singular for that reason; it renders
into the single-element `schedulers` list the API expects.

## Activation also enables CDC

Setting `activate = true` makes the provider run, in order:

1. `disable` — only if the flow is currently active.
2. `update` (PUT) — a data flow created through the API has to be updated once before the activate
   call will accept it. Skip this and activation fails with `RVR-ACTIVATE-500`.
3. `enable_cdc` — for a log-based flow. Nothing else sets `ENABLE_LOG` on a flow that is CDC from
   its very first apply. The endpoint answers `204` when CDC is already enabled, so it is safe to
   repeat.
4. `activate`.

`activate = false` disables the flow if it is currently active. Omitting `activate` leaves
activation unmanaged — there is deliberately no default.

## Seeding or resetting the source offset

The offset — the position the next run resumes from — is managed by the separate
[`boomi_data_integration_data_flow_cdc_config`](../resources/data_integration_data_flow_cdc_config)
resource. One offset exists per data flow; the wire body is `{"config": {…}}` with a
`datasource_type` discriminator selecting the source family:

| `datasource_type` | Offset field(s) |
| --- | --- |
| `mysql` | `binlog_file` + `binlog_position`, or `gtid` |
| `postgres` | `lsn_offset` (integer) — `last_updated` is also required by the API |
| `mssql` | `lsn_offset_sql_server` — a single LSN string, or a map of capture-instance → LSN |
| `mongodb` | `resume_token` |
| `oracle` | `scn_offset` (integer) |

The offset **advances on every run**. It is operational state, so the resource is deliberately
*config-authoritative*: use it to seed a starting position or to reset one, not to continuously
track where the flow has got to. Terraform will not report the server's advancing offset as drift on
your desired-state attributes.

The API returns a `400` from the offset `GET` — not a `404` — while the data flow is CDC but no
offset has materialised yet (nothing has been read from the log). The provider treats that as
"nothing to read".
