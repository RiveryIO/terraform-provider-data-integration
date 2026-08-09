# Managed connection resources.
# If you already manage your connections elsewhere, remove this file and
# replace references like `boomi_data_integration_connection.jira.id` with
# your own resource attribute or a data source lookup.

resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url  = var.jira_base_url
    username  = var.jira_username
    api_token = var.jira_api_token
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account   = var.snowflake_account
    username  = var.snowflake_username
    password  = var.snowflake_password
    warehouse = var.snowflake_warehouse
    database  = var.snowflake_database
    schema    = var.snowflake_schema
    role      = var.snowflake_role
  })
}
