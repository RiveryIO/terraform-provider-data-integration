# Discover every target a data flow can write to — read from the live API,
# so new targets appear with no provider release.
data "boomi_data_integration_target_types" "all" {}

# The id to use as a data flow's properties.target.name.
output "target_type_ids" {
  value = [for t in data.boomi_data_integration_target_types.all.target_types : t.target_type]
}

# Which connection type each target binds to.
output "target_connection_types" {
  value = { for t in data.boomi_data_integration_target_types.all.target_types : t.target_type => t.connection_type }
}
