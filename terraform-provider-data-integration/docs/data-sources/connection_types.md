# boomi_data_integration_connection_types (Data Source)

The catalog of connection types supported by the Data Integration API, read
directly from the **live** API. New connector types appear automatically — no
provider release required. Use it to discover the `type` value for a
`boomi_data_integration_connection`, and pair it with the `boomi_data_integration_connection_type` data source to
discover a type's configurable fields.

## Example Usage

```hcl
data "boomi_data_integration_connection_types" "all" {}

output "available_types" {
  value = [for t in data.boomi_data_integration_connection_types.all.connection_types : t.connection_type]
}
```

## Schema

### Read-Only

- `id` (String) — Static identifier for this catalog data source.
- `connection_types` (List of Object) — All available connection types, sorted by `connection_type`. Each element has:
  - `connection_type` (String) — The identifier used as `boomi_data_integration_connection.type` (e.g. `mysql`).
  - `connection_type_name` (String) — Human-readable name (e.g. `MySQL`).
  - `is_test_connection` (Boolean) — Whether the type supports a test-connection action.
