resource "boomi_data_integration_data_flow" "mysql_to_snowflake" {
  environment_id = var.environment_id
  name           = "sales-to-warehouse"
  type           = "source_to_target"
  activate       = true

  settings = {
    run_timeout_seconds = 43200
    notification = {
      failure = {
        email      = "data-oncall@example.com"
        is_enabled = true
      }
    }
  }

  schedule = {
    cron_expression = "0 * * * *"
    is_enabled      = true
  }

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mysql"
      run_type      = "multi_tables"
      connection_id = boomi_data_integration_connection.source.id
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.warehouse.id
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
    }
    schemas = [{
      name = "sales"
      tables = [{
        details = {
          name           = "customers"
          is_selected    = true
          target_table   = "customers"
          extract_method = "all"
        }
      }]
    }]
  })
}
