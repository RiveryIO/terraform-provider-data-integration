# Example: MySQL CDC → Snowflake

Two CDC variants side-by-side: full snapshot then streaming
(`table_status = "new_table"`) and streaming-only from the current binlog
position (`table_status = "tracked"`).

See [docs/guides/cdc-data-flows.md](../../docs/guides/cdc-data-flows.md) for
field-level reference and when to use each variant.

## Usage

```bash
terraform init
terraform plan
terraform apply
```
