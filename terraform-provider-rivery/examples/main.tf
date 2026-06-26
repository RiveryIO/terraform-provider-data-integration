terraform {
  required_providers {
    rivery = {
      source = "boomi/rivery"
    }
  }
}

# Credentials come from DATA_INTEGRATION_API_TOKEN / _ACCOUNT_ID / _API_URL,
# or set them inline here (token is sensitive — prefer the environment).
provider "rivery" {
  # api_url    = "https://api.rivery.io"
  # token      = var.rivery_token
  # account_id = var.rivery_account_id
}

# An environment groups connections and data flows. Account-scoped.
resource "rivery_environment" "prod" {
  name        = "production"
  description = "Managed by terraform-provider-rivery"
}

# A connection lives in an environment. Credentials go in parameters_json and
# are treated as write-only (never read back from the API).
resource "rivery_connection" "warehouse" {
  environment_id = rivery_environment.prod.id
  name           = "snowflake-prod"
  type           = "snowflake"

  parameters_json = jsonencode({
    account  = "xy12345.us-east-1"
    username = "BOOMI_SVC"
    password = var.snowflake_password
    database = "ANALYTICS"
  })
}

# A data flow (the API calls this a "river"). The flow definition is supplied
# as JSON, keeping the resource forward-compatible with the full river schema.
# It references both the environment and the connection above.
resource "rivery_data_flow" "daily_load" {
  environment_id = rivery_environment.prod.id
  name           = "daily-warehouse-load"
  description    = "Loads the daily batch into ${rivery_connection.warehouse.name}"
  type           = "logic"

  properties_json = jsonencode({
    properties_type = "logic"
    logic_steps = [
      {
        type            = "river"
        name            = "load-step"
        river_id        = var.sub_river_id
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

variable "sub_river_id" {
  type        = string
  description = "cross_id of an existing river used as the logic leaf step"
  default     = ""
}

output "environment_id" {
  value = rivery_environment.prod.id
}

output "data_flow_id" {
  value = rivery_data_flow.daily_load.id
}
