# Jira → Snowflake: two data flows via local modules.
#
# Plug in your Jira and Snowflake credentials, choose a target database,
# and both flows are ready. Everything else (column schema, extract settings,
# loading method) is pre-configured inside each module.
#
#   jira_regular    — syncs the Jira "issue" report to one table (regular run)
#   jira_predefined — syncs one or more Jira built-in reports (user, sprint, …)

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

# ── Flow 1: regular — Jira Issues → one Snowflake table ──────────────────────

module "jira_regular" {
  source = "./modules/jira-to-snowflake"

  jira_connection_id      = boomi_data_integration_connection.jira.id
  snowflake_connection_id = boomi_data_integration_connection.snowflake.id
  target_database         = var.snowflake_database
  target_table            = "jira_issues"
  activate                = var.activate
}

# ── Flow 2: predefined reports — user, sprint, project, … ────────────────────

module "jira_predefined" {
  source = "./modules/jira-predefined-report-to-snowflake"

  jira_connection_id      = boomi_data_integration_connection.jira.id
  snowflake_connection_id = boomi_data_integration_connection.snowflake.id
  target_database         = var.snowflake_database
  activate                = var.activate
}

# ── Outputs ───────────────────────────────────────────────────────────────────

output "jira_connection_id"       { value = boomi_data_integration_connection.jira.id }
output "snowflake_connection_id"  { value = boomi_data_integration_connection.snowflake.id }
output "jira_regular_flow_id"     { value = module.jira_regular.id }
output "jira_predefined_flow_id"  { value = module.jira_predefined.id }
