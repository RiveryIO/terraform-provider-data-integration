# Jira (regular) → Snowflake module.
#
# Fetches a single Jira report (default: "issue") on each run and loads it
# into one Snowflake table.
#
# Column metadata for regular Jira flows lives in
# target.single_table_settings.mapping (NOT in schemas.tables — the API
# rejects run_type_and_datasource = "regular" there).
# The default 147-column schema matches the standard Jira "issue" layout
# and is defined in columns.tf to keep this file readable.

module "dataflow" {
  source = "github.com/RiveryIO/terraform-data-integration-dataflow"

  name        = var.name
  description = "Jira ${var.jira_report} → Snowflake, managed by Terraform"
  activate    = var.activate

  data_flow_source = {
    connection_id = var.jira_connection_id
    type          = "jira"
    run_type      = "regular"
    settings = {
      connection_type       = "jira"
      extract_method        = "all"
      report                = var.jira_report
      keep_raw_customfields = var.keep_raw_customfields
      utc_offset            = var.utc_offset
      time_period           = var.time_period
      start_date            = var.time_period == "date_range" ? var.start_date : null
      required_mapping_flag = false
    }
  }

  target = {
    connection_id  = var.snowflake_connection_id
    type           = "snowflake"
    loading_method = var.loading_method
    database_name  = var.target_database
    schema_name    = var.target_schema
    table_name     = var.target_table
    single_table_settings = {
      mapping = var.columns != null ? var.columns : local.default_issue_columns
    }
  }

  schemas = []
}

output "id" {
  description = "Cross ID of the created data flow."
  value       = module.dataflow.id
}

output "name" {
  value = module.dataflow.name
}
