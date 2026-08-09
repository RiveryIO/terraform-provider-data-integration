# Jira Issues → Snowflake (regular run).
# Syncs the "issue" report into a single table on every run.
# Column schema and extract settings are pre-configured in the module.

module "jira_regular" {
  source = "./modules/jira-to-snowflake"

  jira_connection_id      = boomi_data_integration_connection.jira.id
  snowflake_connection_id = boomi_data_integration_connection.snowflake.id
  target_database         = var.snowflake_database
  target_table            = "jira_issues"
  activate                = var.activate
}

output "jira_regular_flow_id" { value = module.jira_regular.id }
