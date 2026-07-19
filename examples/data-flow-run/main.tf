terraform {
  required_providers {
    boomi = {
      source = "riveryio/data-integration"
    }
  }
}

provider "boomi" {
  # api_url / token / account_id / environment_id from DATA_INTEGRATION_* env vars
}

# Run an existing data flow (river) through Terraform. This models the API's
# imperative activate_river + run actions as a resource (Terraform provider
# Actions require Terraform >= 1.14, so a resource is the portable form).
resource "boomi_data_integration_data_flow_run" "nightly" {
  data_flow_id = var.data_flow_id

  # Change any trigger value to fire another run on the next apply
  # (same pattern as null_resource.triggers).
  triggers = {
    # run on every apply:
    ts = timestamp()
    # or gate on upstream config: config_hash = sha1(jsonencode(local.river_props))
  }
}

variable "data_flow_id" {
  type        = string
  description = "cross_id of the data flow (river) to run."
}

output "run_id" {
  value = boomi_data_integration_data_flow_run.nightly.run_id
}
