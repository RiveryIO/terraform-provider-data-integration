# boomi_data_integration_connection_type (Data Source)

The property schema of a single connection type, read from the **live** Data
Integration API. Use it to discover which fields belong in a `boomi_data_integration_connection`'s
`parameters_json` for a given type. The field set stays current as the API
evolves, with no provider release required.

Because each connection type's property shape differs, the full schema is exposed
as raw JSON (`properties_json`); `property_names` extracts the field ids for
convenience.

## Example Usage

```hcl
data "boomi_data_integration_connection_type" "mysql" {
  connection_type = "mysql"
}

output "mysql_fields" {
  value = data.boomi_data_integration_connection_type.mysql.property_names
  # => ["connection_name","host","port","database","username","password", ...]
}

# Author a connection using the discovered fields
resource "boomi_data_integration_connection" "src" {
  name = "prod-mysql"
  type = data.boomi_data_integration_connection_type.mysql.connection_type
  parameters_json = jsonencode({
    host     = "db.example.com"
    port     = 3306
    username = "svc"
    password = var.mysql_password
    database = "app"
  })
}
```

## Schema

### Required

- `connection_type` (String) — The connection type to look up (e.g. `mysql`, `postgres`, `snowflake`).

### Read-Only

- `id` (String) — Equals `connection_type`.
- `connection_type_name` (String) — Human-readable name (e.g. `MySQL`).
- `property_names` (List of String) — The ids of the type's configurable properties — the keys valid in a `boomi_data_integration_connection` `parameters_json` for this type.
- `properties_json` (String) — The full property schema as raw JSON (a list of property objects, each with `id`/`type` and optional `display_name`/`ui_type`/`default_value`/etc.).
