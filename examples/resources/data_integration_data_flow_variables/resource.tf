resource "boomi_data_integration_data_flow_variables" "example" {
  data_flow_id = boomi_data_integration_data_flow.issues_flow.id

  variable {
    name  = "lookback_days"
    value = "7"
  }

  variable {
    name           = "allowed_statuses"
    value          = "[\"Open\",\"In Progress\"]"
    is_multi_value = true
  }

  variable {
    name         = "api_secret"
    value        = var.api_secret
    is_encrypted = true
  }
}
