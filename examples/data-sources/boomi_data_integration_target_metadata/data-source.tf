# Discover the top-level containers of a TARGET warehouse connection — Snowflake
# databases, BigQuery datasets, or Databricks catalogs — from the live
# connection, so a data flow's target location is chosen from the real warehouse
# rather than hand-typed. Verified against Snowflake in integration (see the
# provider README).

# List the databases of a Snowflake target connection.
data "boomi_data_integration_target_metadata" "warehouse" {
  environment_id = var.environment_id
  connection_id  = boomi_data_integration_connection.snowflake_target.id
  target_type    = "snowflake" # snowflake -> databases, bigquery -> datasets, databricks -> catalogs

  timeouts {
    read = "3m"
  }
}

# `names` is the flat list of database/dataset/catalog names.
output "warehouse_databases" {
  value = data.boomi_data_integration_target_metadata.warehouse.names
}

# `result_json` is the raw operation result, passed through unmodified — decode
# it with jsondecode() for warehouses that return an array of objects.
output "warehouse_result_raw" {
  value = jsondecode(data.boomi_data_integration_target_metadata.warehouse.result_json)
}
