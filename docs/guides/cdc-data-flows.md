---
page_title: "CDC data flows"
subcategory: "Extract methods"
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

Exactly one scheduler is allowed per CDC flow.

## Activation

`activate = true` triggers an extra step for CDC flows: the provider
validates that the source can stream changes and starts the
change-data-capture process before the final activation call — this is why
CDC flows take longer to activate than others. See
[Activation](./activation.md) for the full three-state behavior and drift
reconciliation, shared by every data flow type.

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

The offset advances on every run. Use `boomi_data_integration_data_flow_cdc_config`
to seed a starting position or reset the offset — not to track the current position
continuously.
