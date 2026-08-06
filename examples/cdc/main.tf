# MySQL (log-based CDC) → Snowflake
#
# CDC flows use type = "source_to_target" but require extra per-table fields
# that control whether the flow starts with a full snapshot or directly from
# the current binlog position.
#
# Two variants are shown:
#
#   "migrate_then_stream"  — full-table snapshot first, then live binlog.
#                            Use when the target table is empty.
#
#   "stream_only"          — starts from the current binlog position immediately.
#                            Use when the target is already populated.

terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 1.0"
    }
  }
}

# Credentials from environment variables:
#   DATA_INTEGRATION_API_TOKEN, DATA_INTEGRATION_ACCOUNT_ID,
#   DATA_INTEGRATION_API_URL, DATA_INTEGRATION_ENVIRONMENT_ID
provider "boomi" {}

# ── Connections ───────────────────────────────────────────────────────────────

resource "boomi_data_integration_connection" "mysql_cdc" {
  name = "MySQL CDC Source"
  type = "mysql_cdc"

  parameters_json = jsonencode({
    host     = "db.example.com"
    port     = 3306
    username = "replication_user"
    password = "..."
    database = "mydb"
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake Target"
  type = "snowflake"

  parameters_json = jsonencode({
    account   = "xy12345.us-east-1"
    username  = "SVC_USER"
    password  = "..."
    database  = "ANALYTICS"
    warehouse = "COMPUTE_WH"
    schema    = "PUBLIC"
  })
}

# ── Variant 1: migrate_then_stream ────────────────────────────────────────────
# Full snapshot of `orders`, then live streaming. Use on empty targets.

resource "boomi_data_integration_data_flow" "orders_cdc_migrate" {
  name     = "MySQL CDC → Snowflake (migrate then stream)"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

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
      schema        = "PUBLIC"
      db            = "ANALYTICS"
    }
    schemas = [{
      name = "mydb"
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
}

# ── Variant 2: stream_only ────────────────────────────────────────────────────
# Start from the current binlog position immediately. Use when the target
# is already populated and you only need incremental CDC going forward.

resource "boomi_data_integration_data_flow" "orders_cdc_stream" {
  name     = "MySQL CDC → Snowflake (stream only)"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

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
      schema        = "PUBLIC"
      db            = "ANALYTICS"
    }
    schemas = [{
      name = "mydb"
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
}
