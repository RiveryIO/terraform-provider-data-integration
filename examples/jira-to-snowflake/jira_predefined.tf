module "jira_predefined" {
  source = "./modules/jira-predefined-report-to-snowflake"

  jira_connection_id      = boomi_data_integration_connection.jira.id
  snowflake_connection_id = boomi_data_integration_connection.snowflake.id
  target_database         = var.snowflake_database
  activate                = var.activate
}

output "jira_predefined_flow_id" { value = module.jira_predefined.id }
