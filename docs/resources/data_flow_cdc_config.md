---
page_title: "boomi_data_integration_data_flow_cdc_config Resource"
description: |-
  The CDC offset configuration for a CDC data flow (river).
---

# boomi_data_integration_data_flow_cdc_config (Resource)

Manages the **CDC offset** for a CDC-enabled data flow (river) — the source
position the next run resumes from. The offset shape is source-specific: MySQL
binlog, Postgres/SQL Server LSN, MongoDB resume token, or Oracle SCN.

~> **Operational state, not declarative config.** The CDC offset advances on
every river run. This resource is therefore **config-authoritative**: the value
is kept from your configuration and never refreshed from the API (refreshing
would show perpetual drift). Use it to **seed or reset** an offset — not to
continuously track the live position. There is no in-place "read" of the live
offset through this resource.

## Example Usage

```terraform
resource "boomi_data_integration_data_flow_cdc_config" "mysql_offset" {
  environment_id = boomi_data_integration_environment.prod.id
  data_flow_id   = boomi_data_integration_data_flow.cdc_river.id

  config_json = jsonencode({
    datasource_type = "mysql"
    binlog_file     = "mysql-bin-changelog.000931"
    binlog_position = "515820321"
  })
}
```

Other source types:

```terraform
# Postgres
config_json = jsonencode({ datasource_type = "postgres", lsn_offset = 43168884936157, last_updated = "2022-10-30T17:06:24Z" })
# SQL Server
config_json = jsonencode({ datasource_type = "mssql", lsn_offset_sql_server = "0x0000004B000009350003" })
# MongoDB
config_json = jsonencode({ datasource_type = "mongodb", resume_token = "{\"_data\":\"8262...\"}" })
# Oracle
config_json = jsonencode({ datasource_type = "oracle", scn_offset = 1234567890123 })
```

## Schema

### Required

- `data_flow_id` (String) cross_id of the CDC data flow (river). Changing it
  forces a new resource.
- `config_json` (String) The CDC offset object as JSON, including a
  `datasource_type` discriminator. Sent to the API wrapped as `{"config": <this>}`.

### Optional

- `environment_id` (String) Environment the data flow belongs to. Falls back to
  the provider-level `environment_id`. Changing it forces a new resource.

### Read-Only

- `id` (String) Resource id. Equals `data_flow_id` (one CDC config per river).

## Behavior notes

- **Create == Update**: both issue a single `POST .../cdc_config` (the API has no
  PUT). The write does not require the river to be CDC-enabled; the API's CDC
  validation is enforced on read, not on set.
- **Delete** removes the stored offset (`DELETE .../cdc_config`); a missing offset
  is treated as already-gone.

## Import

```shell
terraform import boomi_data_integration_data_flow_cdc_config.mysql_offset <environment_id>/<data_flow_id>
```
