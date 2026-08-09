# Jira predefined report → Snowflake module.
#
# Runs one or more Jira built-in reports (user, project, sprint, board, …)
# and loads each into its own Snowflake table.

module "dataflow" {
  source = "github.com/RiveryIO/terraform-data-integration-dataflow"

  name        = var.name
  description = "Jira predefined reports → Snowflake, managed by Terraform"
  activate    = var.activate

  data_flow_source = {
    connection_id = var.jira_connection_id
    type          = "jira"
    run_type      = "predefined_report"
    settings = {
      connection_type       = "jira"
      extract_method        = "all"
      keep_raw_customfields = false
      utc_offset            = var.utc_offset
      required_mapping_flag = false
    }
  }

  target = {
    connection_id  = var.snowflake_connection_id
    type           = "snowflake"
    loading_method = "merge"
    merge_method   = "merge"
    database_name  = var.target_database
    schema_name    = var.target_schema
    table_prefix   = var.target_table_prefix
  }

  schemas = [{
    schema_name = "no_schema"
    tables = [
      for r in var.reports : {
        table_name = r.report_name
        details = {
          table_name       = r.report_name
          target_table     = r.target_table
          extract_method   = "all"
          is_selected      = true
          table_status     = "tracked"
          time_period      = r.time_period
          start_date       = r.time_period == "date_range" ? r.start_date : null
          last_days        = r.last_days
          target_loading   = "merge"
          sync_schema_back = false
        }
        run_type_and_datasource = "predefined_report"
      }
    ]
  }]
}

output "id" {
  description = "Cross ID of the created data flow."
  value       = module.dataflow.id
}

output "name" {
  value = module.dataflow.name
}
