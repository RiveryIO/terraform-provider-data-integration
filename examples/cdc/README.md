# Example: MySQL CDC → Snowflake

Demonstrates **Change Data Capture** (log-based CDC) from MySQL into
Snowflake using two variants of the same table, both in one configuration.

## What it creates

| Resource | Purpose |
|---|---|
| `boomi_data_integration_connection.mysql_cdc` | MySQL CDC source (type `mysql_cdc`) |
| `boomi_data_integration_connection.snowflake` | Snowflake target |
| `boomi_data_integration_data_flow.orders_cdc_migrate` | CDC flow — full snapshot then live streaming |
| `boomi_data_integration_data_flow.orders_cdc_stream` | CDC flow — streaming only from current binlog position |

## CDC variants

### `migrate_then_stream` — `table_status = "new_table"`, `initiate_table = true`

Performs a full-table snapshot into Snowflake first, then switches to live
binlog streaming. Use this when:
- The Snowflake target table is empty
- You need historical data before going live

### `stream_only` — `table_status = "tracked"`, `initiate_table = false`

Starts from the current binlog position immediately with no snapshot. Use this when:
- The target is already populated (e.g. backfilled via a separate migration)
- You only need incremental changes going forward

## Key CDC fields

```hcl
details = {
  table_status = "new_table"   # "new_table" = snapshot + stream; "tracked" = stream only
  cdc_settings = {
    initiate_table = true      # true = run snapshot; false = skip snapshot
    overwrite_table_in_migration = false
  }
}
```

## Prerequisites

The MySQL user must have replication privileges:
```sql
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'replication_user'@'%';
GRANT SELECT ON mydb.* TO 'replication_user'@'%';
```

Binary logging must be enabled (`binlog_format = ROW`).

## Usage

```bash
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your credentials
terraform init
terraform plan
terraform apply
```
