# A report delivered by email instead of loaded into a warehouse.
# `target_email` is the one target that needs no connection at all — the
# result lands in the account's file zone and is emailed as a download link.

resource "boomi_data_integration_connection" "api_source" {
  name = "Example API source"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "..."
  })
}

resource "boomi_data_integration_data_flow" "report_to_email" {
  name     = "weekly-report-to-email"
  type     = "source_to_target"
  activate = true

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.api_source.id
      run_type      = "regular"
      additional_settings = {
        report = "issue"
      }
    }
    target = {
      name       = "target_email"
      email_list = ["alerts@example.com"]
    }
    schemas = []
  })
}
