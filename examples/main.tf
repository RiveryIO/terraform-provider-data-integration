terraform {
  required_providers {
    boomi = {
      source = "riveryio/data-integration"
    }
  }
}

# Credentials come from DATA_INTEGRATION_API_TOKEN / _ACCOUNT_ID / _API_URL,
# or set them inline here (token is sensitive — prefer the environment).
provider "boomi" {
  # api_url    = "https://api.rivery.io"
  # token      = var.boomi_data_integration_token
  # account_id = var.boomi_data_integration_account_id
}

# An environment groups connections and data flows. Account-scoped.
resource "boomi_data_integration_environment" "prod" {
  name        = "production"
  description = "Managed by terraform-provider-data-integration"
}

# A connection lives in an environment. Credentials go in parameters_json and
# are treated as write-only (never read back from the API).
resource "boomi_data_integration_connection" "warehouse" {
  environment_id = boomi_data_integration_environment.prod.id
  name           = "snowflake-prod"
  type           = "snowflake"

  parameters_json = jsonencode({
    account_name          = "xy12345.us-east-1"
    username              = "BOOMI_SVC"
    password              = var.snowflake_password
    default_database_name = "ANALYTICS"
  })
}

# A data flow (the API calls this a "river"). The flow definition is supplied
# as JSON, keeping the resource forward-compatible with the full data flow schema.
# It references both the environment and the connection above.
resource "boomi_data_integration_data_flow" "daily_load" {
  environment_id = boomi_data_integration_environment.prod.id
  name           = "daily-warehouse-load"
  description    = "Loads the daily batch into ${boomi_data_integration_connection.warehouse.name}"
  type           = "logic"

  properties_json = jsonencode({
    properties_type = "logic"
    logic_steps = [
      {
        type            = "river"
        name            = "load-step"
        river_id        = var.sub_data_flow_id
        input_variables = {}
      }
    ]
  })
}

variable "snowflake_password" {
  type      = string
  sensitive = true
  default   = ""
}

variable "sub_data_flow_id" {
  type        = string
  description = "cross_id of an existing data flow used as the logic leaf step"
  default     = ""
}

output "environment_id" {
  value = boomi_data_integration_environment.prod.id
}

output "data_flow_id" {
  value = boomi_data_integration_data_flow.daily_load.id
}
