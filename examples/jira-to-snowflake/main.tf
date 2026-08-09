# Jira → Snowflake — two data flows, two module calls.
#
# Supply the two connections and a target database; everything else
# (extract settings, column schema, loading method, …) is pre-configured
# inside each module.

terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 2.0"
    }
  }
}

# Credentials via env vars (preferred):
#   DATA_INTEGRATION_API_TOKEN
#   DATA_INTEGRATION_ACCOUNT_ID
#   DATA_INTEGRATION_ENVIRONMENT_ID
provider "boomi" {
  api_url        = var.api_url
  token          = var.api_token
  account_id     = var.account_id
  environment_id = var.environment_id
}

# ── Connections ───────────────────────────────────────────────────────────────

resource "boomi_data_integration_connection" "jira" {
  environment_id = var.environment_id
  name           = "Jira"
  type           = "jira"

  parameters_json = jsonencode({
    base_url  = var.jira_base_url
    username  = var.jira_username
    api_token = var.jira_api_token
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  environment_id = var.environment_id
  name           = "Snowflake"
  type           = "snowflake"

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

# ── Flow 1: Jira Issues → one Snowflake table (regular) ──────────────────────

module "jira_regular" {
  source = "./modules/jira-to-snowflake"

  jira_connection_id      = boomi_data_integration_connection.jira.id
  snowflake_connection_id = boomi_data_integration_connection.snowflake.id
  target_database         = var.snowflake_database
  target_table            = "jira_issues"
}

# ── Flow 2: Jira predefined reports → one table per report ───────────────────

module "jira_predefined" {
  source = "./modules/jira-predefined-report-to-snowflake"

  jira_connection_id      = boomi_data_integration_connection.jira.id
  snowflake_connection_id = boomi_data_integration_connection.snowflake.id
  target_database         = var.snowflake_database
}

# ── Outputs ───────────────────────────────────────────────────────────────────

output "jira_connection_id" {
  value = boomi_data_integration_connection.jira.id
}

output "snowflake_connection_id" {
  value = boomi_data_integration_connection.snowflake.id
}

output "jira_regular_flow_id" {
  description = "Cross ID of the regular Jira issues data flow."
  value       = module.jira_regular.id
}

output "jira_predefined_flow_id" {
  description = "Cross ID of the Jira predefined reports data flow."
  value       = module.jira_predefined.id
}
