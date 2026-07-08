# boomi_target_types (Data Source)

The catalog of target types supported by the Data Integration API, read directly
from the **live** API. New targets appear automatically — no provider release
required. Use it to discover the `target_type` that goes in a source-to-target
data flow's `properties.target.name`, and the `connection_type` each target binds
to.

> Backed by `GET /v1/target_types`.

## Example Usage

```hcl
data "boomi_target_types" "all" {}

output "target_type_ids" {
  value = [for t in data.boomi_target_types.all.target_types : t.target_type]
}
```

## Schema

### Read-Only

- `id` (String) — Static identifier for this catalog data source.
- `target_types` (List of Object) — All available target types, sorted by `target_type`. Each element has:
  - `target_type` (String) — Target type identifier (e.g. `snowflake`, `bq`) — used in a data flow's `properties.target.name`.
  - `name` (String) — Human-readable name (e.g. `Google BigQuery (Target)`).
  - `connection_type` (String) — The connection type this target binds to (use with `boomi_connection.type`).
  - `logic_step_type` (String) — The logic-step type id associated with this target.
  - `data_flow_type_id` (String) — The data-flow type this target applies to (e.g. `src_to_trgt`).
