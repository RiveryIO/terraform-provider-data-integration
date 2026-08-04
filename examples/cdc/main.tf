# MySQL Aurora (log-based CDC) → Snowflake
#
# CDC flows use type = "source_to_target" but require extra per-table fields
# that control whether the flow starts with a full snapshot or directly from
# the current binlog position.
#
# This file demonstrates two variants in one configuration:
#
#   "migrate_then_stream"  — full-table snapshot first, then live binlog.
#                            Use when the Snowflake target table is empty.
#
#   "stream_only"          — starts from the current binlog position immediately.
#                            Use when the target is already populated.
#
# The CDC settings live inside each table's `details` block. The key fields
# are `table_status`, `initiate_table`, and `cdc_settings`.

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

resource "boomi_data_integration_connection" "mysql_cdc" {
  environment_id = var.environment_id
  name           = "MySQL Aurora CDC Source"
  type           = "mysql_cdc"

  parameters_json = jsonencode({
    host     = var.mysql_host
    port     = var.mysql_port
    username = var.mysql_username
    password = var.mysql_password
    database = var.mysql_database
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  environment_id = var.environment_id
  name           = "Snowflake Target"
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

# ── Variant 1: migrate_then_stream ────────────────────────────────────────────
# Full snapshot of `orders`, then live streaming. Use on empty targets.

resource "boomi_data_integration_data_flow" "orders_cdc_migrate" {
  environment_id = var.environment_id
  name           = "MySQL CDC → Snowflake (migrate then stream)"
  kind           = "main_river"
  type           = "source_to_target"
  activate       = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mysql_cdc"
      connection_id = boomi_data_integration_connection.mysql_cdc.id
      run_type      = "multi_tables"
      cdc_settings  = null
      additional_settings = {
        run_type    = "multi_tables"
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
      name = var.mysql_database
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name                       = "orders"
          target_table               = "orders_cdc"
          is_selected                = true
          is_custom_incremental      = false
          table_status               = "new_table"      # full snapshot first
          exporter_chunk_size        = 30000
          modified_columns           = []
          incremental_field          = null
          date_range                 = null
          running_number             = null
          epoch                      = null
          change_tracking_settings   = null
          system_versioning_settings = null
          additional_target_settings = null
          additional_source_settings = null
          cdc_settings = {
            initiate_table               = true    # run snapshot before streaming
            overwrite_table_in_migration = false
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

# ── Variant 2: stream_only ────────────────────────────────────────────────────
# Start from the current binlog position immediately. Use when the target
# is already populated and you only need incremental CDC going forward.

resource "boomi_data_integration_data_flow" "orders_cdc_stream" {
  environment_id = var.environment_id
  name           = "MySQL CDC → Snowflake (stream only)"
  kind           = "main_river"
  type           = "source_to_target"
  activate       = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mysql_cdc"
      connection_id = boomi_data_integration_connection.mysql_cdc.id
      run_type      = "multi_tables"
      cdc_settings  = null
      additional_settings = {
        run_type    = "multi_tables"
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
      name = var.mysql_database
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name                       = "orders"
          target_table               = "orders_cdc_stream"
          is_selected                = true
          is_custom_incremental      = false
          table_status               = "tracked"        # already populated; no snapshot
          exporter_chunk_size        = 30000
          modified_columns           = []
          incremental_field          = null
          date_range                 = null
          running_number             = null
          epoch                      = null
          change_tracking_settings   = null
          system_versioning_settings = null
          additional_target_settings = null
          additional_source_settings = null
          cdc_settings = {
            initiate_table               = false   # skip snapshot
            overwrite_table_in_migration = false
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
