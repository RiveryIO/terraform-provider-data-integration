# Discover every source a data flow can extract from — read from the live
# API, so new sources appear with no provider release.
data "boomi_data_integration_source_types" "all" {}

# The id to use as a data flow's properties.source.name.
output "source_type_ids" {
  value = [for s in data.boomi_data_integration_source_types.all.source_types : s.id]
}

output "enabled_source_ids" {
  value = [for s in data.boomi_data_integration_source_types.all.source_types : s.id if s.status == "enabled"]
}

# Which connection type each source binds to.
output "source_connection_types" {
  value = { for s in data.boomi_data_integration_source_types.all.source_types : s.id => s.connection_type }
}
