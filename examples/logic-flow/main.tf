# Logic river authored with the boomi/data-integration provider. A logic river
# orchestrates other rivers as ordered steps; this one runs a single leaf step —
# an existing river (e.g. the source-to-target river from examples/source-to-target)
# referenced by its cross_id. The river body is opaque JSON (`properties_json`)
# matching the exact logic-river schema the Data Integration API accepts
# (verified end-to-end on a devbox).

terraform {
  required_providers {
    boomi = {
      source = "boomi/data-integration"
    }
  }
}

provider "boomi" {
  api_url        = var.api_url
  token          = var.api_token
  account_id     = var.account_id
  environment_id = var.environment_id
}

# A logic data flow. It reuses an EXISTING environment (var.environment_id) rather
# than creating one, so no per-environment token re-scoping is required.
resource "boomi_data_integration_data_flow" "logic" {
  environment_id = var.environment_id
  name           = var.river_name
  description    = "Logic river orchestrating a sub-river, managed by terraform-provider-data-integration"
  type           = "logic"
  kind           = "main_river"

  settings_json = jsonencode({})

  # logic_steps is an ordered list of steps. A "river" step runs another river
  # (river_id = its cross_id). Add more steps here to build a real pipeline.
  properties_json = jsonencode({
    properties_type = "logic"
    logic_steps = [
      {
        type            = "river"
        name            = var.step_name
        river_id        = var.sub_river_id
        input_variables = {}
      }
    ]
  })
}

output "logic_river_id" {
  description = "cross_id of the created logic river."
  value       = boomi_data_integration_data_flow.logic.id
}
