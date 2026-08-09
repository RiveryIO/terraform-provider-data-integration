# DEPRECATED EXAMPLE
#
# boomi_data_integration_data_flow_run is deprecated and will be removed in a
# future major version. Terraform manages desired state; it does not run flows.
# Trigger runs from the data flow's own schedule (schedulers_json on
# boomi_data_integration_data_flow, or the platform scheduler), or with a direct
# API call from your orchestrator / CI pipeline. Activation stays in Terraform as
# the `activate` attribute on boomi_data_integration_data_flow.
#
# This example is kept only to document the existing resource for configurations
# that still use it.

# Run an existing data flow through Terraform. This models the API's
# imperative activate_river + run actions as a resource (Terraform provider
# Actions require Terraform >= 1.14, so a resource is the portable form).
resource "boomi_data_integration_data_flow_run" "nightly" {
  data_flow_id = var.data_flow_id

  # Change any trigger value to fire another run on the next apply
  # (same pattern as null_resource.triggers).
  triggers = {
    # run on every apply:
    ts = timestamp()
    # or gate on upstream config: config_hash = sha1(jsonencode(local.data_flow_props))
  }
}

variable "data_flow_id" {
  type        = string
  description = "cross_id of the data flow to run."
}

output "run_id" {
  value = boomi_data_integration_data_flow_run.nightly.run_id
}
