# Minimal data flow: Jira Issues → Snowflake (full load).
# Shows the required structure of properties_json for a source_to_target flow.

resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  # Property names come from GET /v1/connections_types/jira — credentials_type
  # "token" requires domain_name + username + api_token. There is no base_url
  # and no password on this connector.
  parameters_json = jsonencode({
    credentials_type = "token"
    domain_name      = "yourorg" # bare subdomain, not a URL
    username         = "user@example.com"
    api_token        = "..."
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
      # An API connector pulling one named report uses run_type "regular"; the
      # flow then writes to the single table named on the target. For an RDBMS
      # source this would be "multi_tables" with a schemas[] table list.
      run_type     = "regular"
      cdc_settings = null
      additional_settings = {
        connection_type = "jira"
        # REQUIRED: the extraction worker asserts on `report`. Naming the report
        # only as a predefined_report table's details.table_name does not
        # populate this, and the run fails with `Missing Entity!`.
        report         = "issue"
        extract_method = "all"
      }
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      # loading_method is REQUIRED on every target.
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
      # On a `regular` flow the destination table is named here.
      table_name = "jira_issues"
    }
    schemas = []
  })
}
