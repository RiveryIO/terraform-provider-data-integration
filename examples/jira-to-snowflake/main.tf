# Jira → Snowflake: two run-type variants.
#
# Jira sources support two run types:
#
#   "single_table" with report_type = "full_table"
#     Pulls every record from a Jira object (issues, projects, users…).
#     Good for initial loads and objects that don't support incremental.
#
#   "single_table" with report_type = "predefined" and time_period
#     Pulls a named Jira report for a rolling window. Supported periods:
#     today, week_to_date, last_week, month_to_date, last_month, last_7_days,
#     last_30_days, last_90_days, last_180_days, last_365_days, all_time.
#
# Both variants share the same connections; choose whichever matches your load pattern.

terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 1.0"
    }
  }
}

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
    base_url = var.jira_base_url   # e.g. https://yourorg.atlassian.net
    username = var.jira_username
    password = var.jira_api_token  # Jira API token (not user password)
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
    database  = var.snowflake_database
    warehouse = var.snowflake_warehouse
    schema    = var.snowflake_schema
  })
}

# ── Variant 1: full table load ─────────────────────────────────────────────────
# Pulls all Jira issues into a Snowflake table. Suitable for an initial load
# or objects where incremental is not supported.

resource "boomi_data_integration_data_flow" "jira_issues_full" {
  environment_id = var.environment_id
  name           = "Jira Issues → Snowflake (full)"
  kind           = "main_river"
  type           = "source_to_target"
  activate       = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      run_type      = "single_table"
      cdc_settings  = null
      additional_settings = {
        source_type = "source_to_target"
      }
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      schema        = var.snowflake_schema
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
          cdc_settings = {
            initiate_table               = null
            overwrite_table_in_migration = null
          }
          additional_source_settings = {
            report_type = "full_table"
          }
        }
      }]
    }]
  })

  settings_json = jsonencode({
    run_timeout_seconds = null
    notification = {
      failure       = { email = var.notification_email, is_enabled = false, execution_time_limit_seconds = null }
      warning       = { email = var.notification_email, is_enabled = false, execution_time_limit_seconds = null }
      run_threshold = { email = var.notification_email, is_enabled = false, execution_time_limit_seconds = 0 }
    }
  })
}

# ── Variant 2: predefined rolling-window report ────────────────────────────────
# Pulls a named Jira report for a rolling time window.
# Update `time_period` as needed.

resource "boomi_data_integration_data_flow" "jira_issues_incremental" {
  environment_id = var.environment_id
  name           = "Jira Issues → Snowflake (week to date)"
  kind           = "main_river"
  type           = "source_to_target"
  activate       = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      run_type      = "single_table"
      cdc_settings  = null
      additional_settings = {
        source_type = "source_to_target"
      }
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      schema        = var.snowflake_schema
      db            = var.snowflake_database
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
          cdc_settings = {
            initiate_table               = null
            overwrite_table_in_migration = null
          }
          additional_source_settings = {
            report_type = "predefined"
            time_period = "week_to_date"
          }
        }
      }]
    }]
  })

  settings_json = jsonencode({
    run_timeout_seconds = null
    notification = {
      failure       = { email = var.notification_email, is_enabled = false, execution_time_limit_seconds = null }
      warning       = { email = var.notification_email, is_enabled = false, execution_time_limit_seconds = null }
      run_threshold = { email = var.notification_email, is_enabled = false, execution_time_limit_seconds = 0 }
    }
  })
}
