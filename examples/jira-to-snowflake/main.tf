# Jira → Snowflake: two run-type variants.
#
# Jira sources support two run types:
#
#   report_type = "full_table"
#     Pulls every record from a Jira object (issues, projects, users…).
#
#   report_type = "predefined" + time_period
#     Pulls a named Jira report for a rolling window. Supported periods:
#     today, week_to_date, last_week, month_to_date, last_month, last_7_days,
#     last_30_days, last_90_days, last_180_days, last_365_days, all_time.

# ── Connections ───────────────────────────────────────────────────────────────

resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "..." # Jira API token (not user password)
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account_name          = "xy12345.us-east-1"
    username              = "SVC_USER"
    password              = "..."
    default_database_name = "ANALYTICS"
    warehouse             = "COMPUTE_WH"
    default_schema_name   = "PUBLIC"
  })
}

# ── Variant 1: full table load ─────────────────────────────────────────────────
# Pulls all Jira issues. Suitable for an initial load or a nightly full refresh.

resource "boomi_data_integration_data_flow" "jira_issues_full" {
  name     = "Jira Issues → Snowflake (full)"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      run_type      = "predefined_report"
      cdc_settings  = null
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake.id
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
    }
    schemas = [{
      name = "no_schema"
      tables = [{
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

# ── Variant 2: predefined rolling-window report ────────────────────────────────
# Pulls a named Jira report for a rolling time window.

resource "boomi_data_integration_data_flow" "jira_issues_weekly" {
  name     = "Jira Issues → Snowflake (week to date)"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      run_type      = "predefined_report"
      cdc_settings  = null
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake.id
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "predefined_report"
        details = {
          table_name                 = "issues"
          target_table               = "jira_issues_weekly"
          is_selected                = true
          extract_method             = "all"
          additional_source_settings = { report_type = "predefined", time_period = "week_to_date" }
        }
      }]
    }]
  })
}
