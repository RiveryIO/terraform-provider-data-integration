resource "boomi_data_integration_data_flow_group" "etl" {
  environment_id = var.environment_id
  name           = "ETL Pipelines"
  color          = "#3399ff"
  icon           = "folder"
}

resource "boomi_data_integration_data_flow" "jira_issues" {
  environment_id = var.environment_id
  group_id       = boomi_data_integration_data_flow_group.etl.id
  name           = "jira-issues-to-snowflake"
  type           = "source_to_target"

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source          = { name = "jira", connection_id = var.jira_connection_id, run_type = "regular" }
    target          = { name = "snowflake", connection_id = var.snowflake_connection_id, loading_method = "overwrite" }
  })
}
