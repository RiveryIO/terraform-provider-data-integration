# Minimal data flow: Jira Issues → Snowflake (full load).
# Shows the required structure of properties_json for a source_to_target flow.

resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "..."
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  # Property names come from GET /v1/connections_types/snowflake — account_name
  # and default_database_name, not the console's "account"/"database" labels.
  parameters_json = jsonencode({
    account_name          = "xy12345.us-east-1"
    username              = "SVC_USER"
    password              = "..."
    default_database_name = "ANALYTICS"
    warehouse             = "COMPUTE_WH"
    default_schema_name   = "PUBLIC"
  })
}

resource "boomi_data_integration_data_flow" "jira_issues" {
  name     = "Jira Issues → Snowflake"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      # Jira exposes predefined reports rather than raw tables, so both the
      # source run_type and each table's run_type_and_datasource below are
      # "predefined_report". For an RDBMS source these would be "multi_tables".
      run_type     = "predefined_report"
      cdc_settings = null
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      # loading_method is REQUIRED on every target.
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        # Only "multi_tables" and "predefined_report" are accepted here — this
        # field is the discriminator that selects the table schema.
        run_type_and_datasource = "predefined_report"
        details = {
          table_name                 = "issues"
          target_table               = "jira_issues"
          is_selected                = true
          extract_method             = "all"
          additional_source_settings = { report_type = "full_table" }
        }
      }]
    }]
  })
}
