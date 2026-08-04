# Complete environment onboarding example.
#
# Shows a realistic getting-started configuration: one environment with
# three connections (Snowflake, Jira, S3) and two data flows
# (Jira→Snowflake full load + Jira→S3 predefined report).
#
# This is the recommended starting point for new teams. Fork it, replace
# the connection parameters with your own, and run:
#
#   terraform init
#   terraform plan
#   terraform apply
#
# After apply, activate the data flows from the Boomi Data Integration UI
# or set `activate = true` on each resource.

terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 1.0"
    }
  }
}

# Credentials are read from environment variables by default:
#   DATA_INTEGRATION_API_TOKEN
#   DATA_INTEGRATION_ACCOUNT_ID
#   DATA_INTEGRATION_API_URL       (optional, defaults to https://api.rivery.io)
#   DATA_INTEGRATION_ENVIRONMENT_ID
#
# Or pass them as variables (set in terraform.tfvars — never commit credentials).
provider "boomi" {
  api_url        = var.api_url
  token          = var.api_token
  account_id     = var.account_id
  environment_id = var.environment_id
}

# ── Connections ───────────────────────────────────────────────────────────────

resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url = var.jira_base_url
    username = var.jira_username
    password = var.jira_api_token
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account   = var.snowflake_account
    username  = var.snowflake_username
    password  = var.snowflake_password
    database  = var.snowflake_database
    warehouse = var.snowflake_warehouse
    schema    = "PUBLIC"
  })
}

resource "boomi_data_integration_connection" "s3" {
  name = "S3 File Zone"
  type = "aws_fz"

  parameters_json = jsonencode({
    aws_access_key    = var.s3_access_key
    aws_access_secret = var.s3_access_secret
    region            = var.s3_region
    bucket_name       = var.s3_bucket
  })
}

# ── Data flows ────────────────────────────────────────────────────────────────

# Full load: all Jira issues → Snowflake.
resource "boomi_data_integration_data_flow" "jira_issues" {
  name     = "Jira Issues → Snowflake"
  kind     = "main_river"
  type     = "source_to_target"
  activate = false   # set true when ready to run

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      run_type      = "single_table"
      cdc_settings  = null
      additional_settings = { source_type = "source_to_target" }
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      schema        = "PUBLIC"
      db            = var.snowflake_database
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "single_table"
        details = {
          name                       = "issues"
          target_table               = "jira_issues"
          is_selected                = true
          is_custom_incremental      = false
          exporter_chunk_size        = 30000
          modified_columns           = []
          incremental_field          = null
          date_range                 = null
          running_number             = null
          epoch                      = null
          change_tracking_settings   = null
          system_versioning_settings = null
          additional_target_settings = null
          cdc_settings               = { initiate_table = null, overwrite_table_in_migration = null }
          additional_source_settings = { report_type = "full_table" }
        }
      }]
    }]
  })

  settings_json = jsonencode({ run_timeout_seconds = null })
}

# Rolling export: last 7 days of Jira issues → S3 as CSV.
resource "boomi_data_integration_data_flow" "jira_weekly_export" {
  name     = "Jira Issues (7 days) → S3"
  kind     = "main_river"
  type     = "source_to_target"
  activate = false

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      run_type      = "single_table"
      cdc_settings  = null
      additional_settings = { source_type = "source_to_target" }
    }
    target = {
      name          = "aws_fz"
      connection_id = boomi_data_integration_connection.s3.id
      bucket_name   = var.s3_bucket
      fz_partition  = "d"
      delimiter     = ","
      source_format = "CSV"
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "single_table"
        details = {
          name                       = "issues"
          target_table               = "jira_issues_weekly"
          is_selected                = true
          is_custom_incremental      = false
          exporter_chunk_size        = 30000
          modified_columns           = []
          incremental_field          = null
          date_range                 = null
          running_number             = null
          epoch                      = null
          change_tracking_settings   = null
          system_versioning_settings = null
          additional_target_settings = null
          cdc_settings               = { initiate_table = null, overwrite_table_in_migration = null }
          additional_source_settings = { report_type = "predefined", time_period = "last_7_days" }
        }
      }]
    }]
  })

  settings_json = jsonencode({ run_timeout_seconds = null })
}

# ── Outputs ───────────────────────────────────────────────────────────────────

output "jira_connection_id"         { value = boomi_data_integration_connection.jira.id }
output "snowflake_connection_id"    { value = boomi_data_integration_connection.snowflake.id }
output "s3_connection_id"           { value = boomi_data_integration_connection.s3.id }
output "jira_issues_flow_id"        { value = boomi_data_integration_data_flow.jira_issues.id }
output "jira_weekly_export_flow_id" { value = boomi_data_integration_data_flow.jira_weekly_export.id }
