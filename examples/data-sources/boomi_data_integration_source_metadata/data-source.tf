# Discover a source's schema/columns from the live connection, then feed the
# discovered mapping straight into a source-to-target data flow — no hand-written
# schemas block. Verified against BigQuery in prod-au (see the provider README).

# Discover the columns of specific tables in a source connection.
data "boomi_data_integration_source_metadata" "sales" {
  environment_id = var.environment_id
  connection_id  = boomi_data_integration_connection.mysql_source.id
  datasource     = "mysql"
  schema         = "sales"
  tables         = ["customers", "orders"] # omit to discover every table in the schema

  timeouts {
    read = "5m" # BigQuery scans the whole catalog and can take several minutes
  }
}

# schemas_json is a ready-to-use properties.schemas[] block — decode it directly
# into a data flow's properties_json.
resource "boomi_data_integration_data_flow" "mysql_to_snowflake" {
  environment_id = var.environment_id
  name           = "sales-to-warehouse"
  type           = "source_to_target"
  kind           = "main_river"

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mysql"
      connection_id = boomi_data_integration_connection.mysql_source.id
      run_type      = "multi_tables"
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake_target.id
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
    }
    # The discovered mapping — tables, target names, and per-column selection.
    schemas = jsondecode(data.boomi_data_integration_source_metadata.sales.schemas_json)
  })
}

# The same discovery is also exposed as typed nested objects for inspection.
output "discovered_columns" {
  value = data.boomi_data_integration_source_metadata.sales.schemas
}
