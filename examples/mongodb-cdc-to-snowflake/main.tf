# MongoDB change-data-capture -> Snowflake: streams changes instead of
# re-querying the whole collection every run. See the CDC data flows guide
# for the mandatory scheduler and its cron bounds.

resource "boomi_data_integration_connection" "mongodb_source" {
  name = "MongoDB"
  type = "mongodb"

  parameters_json = jsonencode({
    host     = "db.example.com"
    port     = 27017
    database = "app"
    username = "readonly"
    password = "..."
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account   = "xy12345.us-east-1"
    username  = "SVC_USER"
    password  = "..."
    database  = "ANALYTICS"
    warehouse = "COMPUTE_WH"
    schema    = "PUBLIC"
  })
}

resource "boomi_data_integration_data_flow" "mongodb_cdc" {
  name     = "mongodb-cdc-to-snowflake"
  type     = "source_to_target"
  activate = true

  # CDC flows require an enabled scheduler before they can be created or activated.
  schedule = {
    cron_expression = "*/15 * * * *"
    is_enabled      = true
  }

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mongodb"
      connection_id = boomi_data_integration_connection.mongodb_source.id
      run_type      = "multi_tables"
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake.id
      loading_method = "merge"
      merge_method   = "merge"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
    }
    schemas = [{
      name = "app"
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name           = "orders"
          target_table   = "orders"
          is_selected    = true
          extract_method = "log"
          table_status   = "new_table"
          cdc_settings = {
            initiate_table               = true
            overwrite_table_in_migration = false
          }
        }
      }]
    }]
  })
}

# Seeds the starting position the first CDC run resumes from.
resource "boomi_data_integration_data_flow_cdc_config" "mongodb_offset" {
  data_flow_id = boomi_data_integration_data_flow.mongodb_cdc.id

  config_json = jsonencode({
    datasource_type = "mongodb"
    resume_token    = ""
  })
}
