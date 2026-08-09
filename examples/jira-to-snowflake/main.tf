# Provider and connections shared by both flows.
# Each flow lives in its own file: jira_regular.tf and jira_predefined.tf.

terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 2.0"
    }
  }
}

# Credentials via environment variables (preferred):
#   DATA_INTEGRATION_API_TOKEN
#   DATA_INTEGRATION_ACCOUNT_ID
#   DATA_INTEGRATION_ENVIRONMENT_ID
provider "boomi" {}

# ── Connections ───────────────────────────────────────────────────────────────

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
