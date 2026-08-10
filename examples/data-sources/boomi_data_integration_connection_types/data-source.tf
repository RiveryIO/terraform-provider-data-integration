# Browse every connection type the live API currently supports — new
# connectors appear here automatically, with no provider release required.
data "boomi_data_integration_connection_types" "all" {}

output "connection_type_count" {
  value = length(data.boomi_data_integration_connection_types.all.connection_types)
}

# The slug to use as a boomi_data_integration_connection resource's `type`.
output "connection_type_ids" {
  value = [for c in data.boomi_data_integration_connection_types.all.connection_types : c.connection_type]
}
