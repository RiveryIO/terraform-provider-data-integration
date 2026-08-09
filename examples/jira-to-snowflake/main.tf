# Jira → Snowflake — two data flows in one example.
#
# This example creates:
#   1. jira_regular   — fetches the Jira "issue" report into a single Snowflake
#                       table (run_type = "regular"). Column schema is pinned to
#                       the standard 147-column Jira issue layout via
#                       target.single_table_settings.mapping in the module default.
#
#   2. jira_predefined — runs one or more Jira built-in reports (user, project,
#                        sprint, …) and loads each into its own Snowflake table
#                        (run_type = "predefined_report").
#
# Both flows use the same Jira and Snowflake connections defined below.
# The two local modules wrap github.com/RiveryIO/terraform-data-integration-dataflow
# with Jira-specific defaults baked in.

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

# ── Flow 1: regular — Jira Issues → one Snowflake table ──────────────────────
#
# run_type = "regular" pulls the chosen Jira report on each run and writes all
# rows to a single target table. The module defaults to the "issue" report with
# a standard 147-column schema; override columns = [...] to narrow or extend it.

module "jira_regular" {
  source = "./modules/jira-to-snowflake"

  jira_connection_id      = boomi_data_integration_connection.jira.id
  snowflake_connection_id = boomi_data_integration_connection.snowflake.id

  name                  = "Jira Issues → Snowflake"
  activate              = var.activate
  jira_report           = "issue"
  keep_raw_customfields = true
  time_period           = "date_range"
  start_date            = "2024-01-01 00:00:00"
  loading_method        = "overwrite"
  target_database       = var.snowflake_database
  target_schema         = var.snowflake_schema
  target_table          = "jira_issues"
  # columns uses the module default: full 147-column standard Jira issue schema.
  # Override with a trimmed list to load only specific fields.
}

# ── Flow 2: predefined_report — multiple Jira reports → multiple tables ───────
#
# run_type = "predefined_report" runs one or more Jira built-in report endpoints.
# Each report writes to its own Snowflake table. Tables are created as:
#   <target_table_prefix><report.target_table>
# e.g. prefix "" + target_table "jira_project" → table "jira_project"

module "jira_predefined" {
  source = "./modules/jira-predefined-report-to-snowflake"

  jira_connection_id      = boomi_data_integration_connection.jira.id
  snowflake_connection_id = boomi_data_integration_connection.snowflake.id

  name                = "Jira Predefined Reports → Snowflake"
  activate            = var.activate
  target_database     = var.snowflake_database
  target_schema       = var.snowflake_schema
  target_table_prefix = ""

  reports = [
    {
      report_name  = "project"
      target_table = "jira_project"
      time_period  = "date_range"
      start_date   = "2024-01-01 00:00:00"
      last_days    = 3
    },
    {
      report_name  = "user"
      target_table = "jira_user"
      time_period  = "date_range"
      start_date   = "2024-01-01 00:00:00"
      last_days    = 3
    },
  ]
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
