# Jira → Snowflake: two extraction-window variants.
#
# Both use run_type = "regular", because the report identifier has to reach the
# extraction worker as source.additional_settings.report. Naming a report only
# as a predefined_report table's details.table_name does NOT populate that key,
# and the run fails at run time with the opaque `Missing Entity!`. See
# guides/api-connector-data-flows.md.
#
#   extract_method = "all"
#     Pulls every record from a Jira report (issue, project, user…).
#
#   extract_method = "all" + time_period
#     Pulls the report for a rolling window. Supported periods:
#     today, week_to_date, last_week, month_to_date, last_month, last_7_days,
#     last_30_days, last_90_days, last_180_days, last_365_days, all_time.
#
# Valid report identifiers come from the live connector catalog:
#   GET .../data_source_properties/global_properties?datasource_id=jira
# For Jira the `reports` map holds: group, group_users, issue, issue_changelogs,
# issue_fields, project, project_category, project_role, project_type,
# resolution, sprint, user, work_logs.

# ── Connections ───────────────────────────────────────────────────────────────

resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  # Fields per GET /v1/connections_types/jira — not base_url/password.
  parameters_json = jsonencode({
    credentials_type = "token"
    domain_name      = "yourorg" # bare subdomain, not a URL
    username         = "user@example.com"
    api_token        = "..." # Jira API token (not the user's password)
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
      run_type      = "regular"
      cdc_settings  = null
      additional_settings = {
        connection_type       = "jira"
        report                = "issue" # the key the worker asserts on
        extract_method        = "all"
        keep_raw_customfields = false
        required_mapping_flag = false
        utc_offset            = 0
      }
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake.id
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
      # On a `regular` flow the destination table is named here, not in schemas.
      table_name = "jira_issues"
    }
    schemas = []
  })
}

# ── Variant 2: rolling-window report ───────────────────────────────────────────
# Same report, restricted to a rolling time window.

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
      run_type      = "regular"
      cdc_settings  = null
      additional_settings = {
        connection_type       = "jira"
        report                = "issue"
        extract_method        = "all"
        time_period           = "week_to_date"
        keep_raw_customfields = false
        required_mapping_flag = false
        utc_offset            = 0
      }
    }
    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake.id
      loading_method = "overwrite"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
      table_name     = "jira_issues_weekly"
    }
    schemas = []
  })
}
