# Discover the full connector catalog (read from the live API).
data "boomi_connection_types" "all" {}

# Discover the configurable fields of one type — the field set stays current
# as the API adds connectors/fields, without changing this provider.
data "boomi_connection_type" "mysql" {
  connection_type = "mysql"
}

output "connection_type_count" {
  value = length(data.boomi_connection_types.all.connection_types)
}

output "mysql_field_ids" {
  value = data.boomi_connection_type.mysql.property_names
}

output "mysql_schema_json" {
  value = data.boomi_connection_type.mysql.properties_json
}
