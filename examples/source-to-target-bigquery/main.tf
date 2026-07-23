# source_to_target river → BigQuery, authored with the riveryio/data-integration
# provider. Modeled on a real integration river (ECB rates → BigQuery). The river
# body is opaque JSON (properties.json) mirroring the exact source_to_target
# structure the API accepts; only the target connection_id + dataset are wired
# from resources/vars.
#
# ⚠️ KNOWN LIMITATION (provider gap): a working BigQuery target needs a `gcloud`
# connection with an uploaded service-account KEY FILE. The provider's JSON-only
# connection create cannot upload a key file, so a river using this connection
# AUTHORS fine but FAILS activation with "Dataset <name> was not found in
# BigQuery" until the connection's key is supplied out-of-band (e.g. the console).
# See README.md.

terraform {
  required_providers {
    boomi = {
      source = "riveryio/data-integration"
    }
  }
}

provider "boomi" {
  api_url        = var.api_url
  token          = var.api_token
  account_id     = var.account_id
  environment_id = var.environment_id
}

resource "boomi_data_integration_connection" "bq" {
  environment_id = var.environment_id
  name           = var.bq_connection_name
  type           = "gcloud"

  parameters_json = jsonencode({
    project_id            = var.gcp_project_id
    service_account_email = var.gcp_service_account_email
    region                = var.gcp_region
    file_type             = "json"
    connection_desc       = "BigQuery target created by terraform"
  })
}

locals {
  base_props = jsondecode(file("${path.module}/properties.json"))

  # Wire the fresh connection id + target dataset into the mirrored properties.
  props = merge(local.base_props, {
    target = merge(local.base_props.target, {
      connection_id = boomi_data_integration_connection.bq.id
      dataset_id    = var.bq_dataset_id
    })
  })
}

resource "boomi_data_integration_data_flow" "s2t" {
  environment_id = var.environment_id
  name           = var.river_name
  type           = "source_to_target"
  kind           = "main_river"

  properties_json = jsonencode(local.props)
  settings_json   = jsonencode({})

  # Route shared connections through the env group (API assigns the default if unset).
  group_id = var.group_id
}

output "data_flow_id" {
  value = boomi_data_integration_data_flow.s2t.id
}

output "bq_connection_id" {
  value = boomi_data_integration_connection.bq.id
}
