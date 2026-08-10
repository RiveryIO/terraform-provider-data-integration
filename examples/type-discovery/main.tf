# Discover the source and target catalogs straight from the live API, so a
# source-to-target data flow can be authored against current, valid values
# without hardcoding — new sources/targets appear with no provider release.
data "boomi_data_integration_source_types" "all" {}
data "boomi_data_integration_target_types" "all" {}

output "source_type_count" {
  value = length(data.boomi_data_integration_source_types.all.source_types)
}

output "enabled_source_ids" {
  value = [for s in data.boomi_data_integration_source_types.all.source_types : s.id if s.status == "enabled"]
}

output "target_type_ids" {
  value = [for t in data.boomi_data_integration_target_types.all.target_types : t.target_type]
}
