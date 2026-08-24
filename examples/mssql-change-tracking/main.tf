# MSSQL Change Tracking → Snowflake
#
# Change tracking (SQL Server native feature) records which rows changed since a
# given sync version — lighter than log-based CDC because it doesn't require
# enabling SQL Server Agent or CDC on the database.
#
# Key per-table settings live in additional_source_settings:
#
#   include_deleted_rows  — whether DELETE operations are propagated to the target.
#                           true  → deleted rows are removed from the target table.
#                           false → only INSERTs and UPDATEs are synced; deletes ignored.
#
#   last_sync_version     — the SQL Server change-tracking version to resume from.
#                           On first run leave it at 0; the platform updates it
#                           automatically after each successful run.
#
# Two variants are shown side by side so the difference is clear.

# ── Connections ───────────────────────────────────────────────────────────────

resource "boomi_data_integration_connection" "mssql" {
  name = "MSSQL Source"
  type = "mssql"

  parameters_json = jsonencode({
    host     = "sqlserver.example.com"
    port     = 1433
    username = "etl_user"
    password = "..."
    database = "sales_db"
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake Target"
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

# ── Variant 1: include_deleted_rows = true ────────────────────────────────────
# Deleted rows are propagated to the target. The target table mirrors the source
# exactly — rows deleted in MSSQL are removed from Snowflake on the next run.

resource "boomi_data_integration_data_flow" "orders_with_deletes" {
  name     = "MSSQL Change Tracking → Snowflake (with deletes)"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mssql"
      connection_id = boomi_data_integration_connection.mssql.id
      run_type      = "multi_tables"
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      schema        = "PUBLIC"
      db            = "ANALYTICS"
    }
    schemas = [{
      name = "test_terraform_schema"
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name                = "orders"
          target_table        = "orders"
          is_selected         = true
          extract_method      = "change_tracking"
          exporter_chunk_size = 30000
          modified_columns    = []

          # ── Key setting ──────────────────────────────────────────────────────
          additional_source_settings = {
            source_type          = "mssql"
            include_deleted_rows = true # DELETE operations propagated to target
            last_sync_version    = 0    # platform updates this after each run
            filter_expression    = ""
          }
          # ─────────────────────────────────────────────────────────────────────

          additional_target_settings = {
            target_type            = "snowflake"
            enforce_masking_policy = false
            is_ordered_merge_key   = false
            recreate_keys          = false
            escape_character       = null
            merge_method           = null
            order_expression       = null
            remove_deleted_rows    = null
            target_loading         = null
          }
          cdc_settings = {
            initiate_table               = false
            overwrite_table_in_migration = null
          }
          change_tracking_settings   = null
          system_versioning_settings = null
          date_range                 = null
          running_number             = null
          epoch                      = null
          incremental_field          = null
          is_custom_incremental      = false
          table_status               = "migrating"
        }
      }]
    }]
  })
}

# ── Variant 2: include_deleted_rows = false ───────────────────────────────────
# Only INSERTs and UPDATEs are synced. Rows deleted in MSSQL remain in the
# target. Use when downstream consumers must retain the full history.

resource "boomi_data_integration_data_flow" "orders_inserts_updates_only" {
  name     = "MSSQL Change Tracking → Snowflake (inserts + updates only)"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mssql"
      connection_id = boomi_data_integration_connection.mssql.id
      run_type      = "multi_tables"
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      schema        = "PUBLIC"
      db            = "ANALYTICS"
    }
    schemas = [{
      name = "test_terraform_schema"
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name                = "orders"
          target_table        = "orders"
          is_selected         = true
          extract_method      = "change_tracking"
          exporter_chunk_size = 30000
          modified_columns    = []

          # ── Key setting ──────────────────────────────────────────────────────
          additional_source_settings = {
            source_type          = "mssql"
            include_deleted_rows = false # deletes ignored; target retains all rows
            last_sync_version    = 0     # platform updates this after each run
            filter_expression    = ""
          }
          # ─────────────────────────────────────────────────────────────────────

          additional_target_settings = {
            target_type            = "snowflake"
            enforce_masking_policy = false
            is_ordered_merge_key   = false
            recreate_keys          = false
            escape_character       = null
            merge_method           = null
            order_expression       = null
            remove_deleted_rows    = null
            target_loading         = null
          }
          cdc_settings = {
            initiate_table               = false
            overwrite_table_in_migration = null
          }
          change_tracking_settings   = null
          system_versioning_settings = null
          date_range                 = null
          running_number             = null
          epoch                      = null
          incremental_field          = null
          is_custom_incremental      = false
          table_status               = "migrating"
        }
      }]
    }]
  })
}
