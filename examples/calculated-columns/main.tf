# Calculated columns (source expressions) — MSSQL → Snowflake
#
# modified_columns can include columns that do not exist in the source table.
# Setting calculated_column_mode = "source" tells the platform to evaluate
# `expression` as a SQL snippet at the source database and land the result
# in the target column under `alias`.
#
# This is the canonical way to enrich every extracted row with metadata such
# as ingestion timestamps, run watermarks, or any deterministic SQL expression
# the source database supports — without touching the source schema.
#
# This example assumes the source and target connections already exist.
# Pass their IDs via variables (or substitute literal string IDs).

variable "environment_id" {
  type        = string
  description = "An existing environment ID."
}

variable "source_connection_id" {
  type        = string
  description = "Cross-ID of an existing MSSQL source connection."
}

variable "target_connection_id" {
  type        = string
  description = "Cross-ID of an existing Snowflake target connection."
}

# ── Data flow ─────────────────────────────────────────────────────────────────

resource "boomi_data_integration_data_flow" "orders_with_expression_columns" {
  environment_id = var.environment_id
  name           = "MSSQL → Snowflake with calculated columns"
  kind           = "main_river"
  type           = "source_to_target"
  activate       = false

  schedule = {
    cron_expression = "0 * * * *"
    is_enabled      = false
  }

  properties_json = jsonencode({
    properties_type = "source_to_target"

    source = {
      name          = "mssql"
      connection_id = var.source_connection_id
      run_type      = "multi_tables"
      additional_settings = {
        run_type       = "multi_tables"
        extract_method = "all"
        fz_batched     = true
        fz_partition   = "d"
        source_format  = "CSV"
      }
    }

    target = {
      name           = "snowflake"
      connection_id  = var.target_connection_id
      loading_method = "merge"
      merge_method   = "merge"
      database_name  = "<SNOWFLAKE_DATABASE>"
      schema_name    = "<SNOWFLAKE_SCHEMA>"
    }

    schemas = [{
      name = "<SOURCE_SCHEMA>"
      tables = [{
        run_type_and_datasource = "multi_tables"
        details = {
          name           = "orders"
          target_table   = "orders"
          is_selected    = true
          extract_method = "all"

          # ── Calculated columns ────────────────────────────────────────────────
          #
          # Each entry with calculated_column_mode = "source" becomes a new column
          # in the target row whose value is the result of `expression` evaluated
          # at the MSSQL source. The column does not need to exist in the source
          # table — the platform injects it into every extracted row via the SELECT.
          #
          # Fields that matter for source expressions:
          #
          #   calculated_column_mode  Must be "source" for server-side expressions.
          #                           ("target" instead computes the expression at
          #                           the warehouse side — a different feature.)
          #
          #   expression              Any SQL scalar expression the source database
          #                           supports. Evaluated once per row.
          #
          #   type                    The target column type used when the table is
          #                           created. TIMESTAMP is common for datetime
          #                           expressions; STRING/INTEGER/FLOAT for others.
          #
          #   alias                   The column name written to the target table.
          #
          #   name                    Matches alias for expression columns (no
          #                           source column to rename from).
          #
          #   order                   Position among expression columns (1-based).
          #                           Does not affect column order in the target DDL.
          #
          #   is_selected             Must be true for the column to be extracted.
          #
          #   target_type             The target connector type ("snowflake" here).
          #                           Used internally for type-mapping lookups.
          # ─────────────────────────────────────────────────────────────────────

          modified_columns = [
            # Regular column: mark as merge key so Snowflake MERGE has a join key.
            # Calculated columns cannot be merge keys — is_key must be false or
            # absent on expression entries.
            {
              name        = "order_id"
              type        = "INTEGER"
              is_selected = true
              is_key      = true
            },

            # Expression column 1: UTC timestamp of the source at extraction time.
            # SYSUTCDATETIME() is MSSQL-specific; substitute GETUTCDATE() for
            # lower precision or NOW() for other databases.
            {
              name                   = "ingestion_utc_at"
              alias                  = "ingestion_utc_at"
              expression             = "SYSUTCDATETIME()"
              type                   = "TIMESTAMP"
              calculated_column_mode = "source"
              is_selected            = true
              order                  = 1
              target_type            = "snowflake"
            },

            # Expression column 2: local server timestamp — useful when the
            # source operates in a specific timezone and you need to preserve it.
            {
              name                   = "ingestion_local_at"
              alias                  = "ingestion_local_at"
              expression             = "GETDATE()"
              type                   = "TIMESTAMP"
              calculated_column_mode = "source"
              is_selected            = true
              order                  = 2
              target_type            = "snowflake"
            },
          ]

          additional_source_settings = {
            source_type       = "mssql"
            filter_expression = ""
          }
          additional_target_settings = {
            target_type = "snowflake"
          }
        }
      }]
    }]
  })
}

# ── Outputs ───────────────────────────────────────────────────────────────────

output "data_flow_id" {
  value = boomi_data_integration_data_flow.orders_with_expression_columns.id
}
