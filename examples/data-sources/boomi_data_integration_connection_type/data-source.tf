# Discover the exact fields one connection type accepts, straight from the
# live API — the field set stays current as the API evolves, with no
# provider release required.
data "boomi_data_integration_connection_type" "snowflake" {
  connection_type = "snowflake"
}

# The keys valid inside a boomi_data_integration_connection resource's
# parameters_json for this type.
output "snowflake_field_ids" {
  value = data.boomi_data_integration_connection_type.snowflake.property_names
}

# The full raw property schema (types, required flags, file-upload fields).
output "snowflake_schema_json" {
  value = data.boomi_data_integration_connection_type.snowflake.properties_json
}
