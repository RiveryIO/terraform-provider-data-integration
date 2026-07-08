# boomi_data_integration_source_types (Data Source)

The catalog of source (datasource) types supported by the Data Integration API,
read directly from the **live** API. New sources appear automatically — no
provider release required. Use it to discover the `id` that goes in a
source-to-target data flow's `properties.source.name`, and the `connection_type`
each source binds to.

> Backed by `GET /v1/data_source_types`.

## Example Usage

```hcl
data "boomi_data_integration_source_types" "all" {}

output "database_sources" {
  value = [
    for s in data.boomi_data_integration_source_types.all.source_types :
    s.id if s.status == "enabled"
  ]
}
```

## Schema

### Read-Only

- `id` (String) — Static identifier for this catalog data source.
- `source_types` (List of Object) — All available source types, sorted by `id`. Each element has:
  - `id` (String) — Source type identifier (e.g. `mysql`, `salesforce`) — the value used in a data flow's `properties.source.name`.
  - `name` (String) — Human-readable name (e.g. `MySQL`).
  - `connection_type` (String) — The connection type this source binds to (use with `boomi_data_integration_connection.type`).
  - `status` (String) — Availability status (e.g. `enabled`, `coming_soon`, `sunset`).
  - `section_id` (String) — Catalog section/category id.
  - `documentation_url` (String) — Link to the source's documentation.
