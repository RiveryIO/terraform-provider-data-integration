# Multi-step logic river authored with the riveryio/data-integration provider.
#
# A logic river orchestrates other steps in order. This example builds a
# `run_once` container with up to three step types:
#
#   1. river               — run an existing river (e.g. the source-to-target river)
#   2. logicode (python)    — run a Python step  [optional; see the file_id note below]
#   3. <warehouse>_sql_query — a SQL / DB transformation writing to a warehouse table
#
# The river body is opaque JSON (`properties_json`), so this config emits the
# exact step shapes the Data Integration API accepts (matching logic_builder.py).
#
# NOTE on the Python step: the API cannot upload code; a `logicode` step must
# reference a `file_id` of an already-uploaded code file (from the UI editor or
# another river). Leave `python_file_id` empty (the default) to omit the Python
# step; set it to a real file_id to include it.
#
# NOTE on group_id: logic rivers that use a shared warehouse connection (the
# Snowflake SQL step below) must set `group_id` to the environment's group
# cross_id, or the connection fails to route at run time (misleading 404). The
# provider exposes `group_id` on the data flow for exactly this reason.

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

# Snowflake connection used by the SQL / DB transformation step.
resource "boomi_data_integration_connection" "snowflake" {
  environment_id = var.environment_id
  name           = var.snowflake_connection_name
  type           = "snowflake"

  parameters_json = jsonencode({
    account_name = var.sf_account_name
    username     = var.sf_username
    password     = var.sf_password
    warehouse    = var.sf_warehouse
    role         = var.sf_role
    database     = var.sf_target_database
  })
}

locals {
  # Step 1: run the existing (source-to-target) river.
  river_step = {
    name            = "run-s2t-subriver"
    type            = "river"
    river_id        = var.sub_river_id
    input_variables = {}
    is_enabled      = true
    disable_errors  = false
  }

  # Step 2 (optional): a Python (logicode) step. Included only when a real
  # uploaded code file_id is supplied — the API cannot create code via Terraform.
  python_steps = var.python_file_id == "" ? [] : [{
    name                = "python-transform"
    type                = "logicode"
    code_type           = "python"
    file_id             = var.python_file_id
    logicode_size       = var.python_size
    additional_packages = var.python_packages
    is_enabled          = true
    disable_errors      = false
  }]

  # Step 3: a Snowflake SQL / DB transformation writing its SELECT to a table.
  sql_step = {
    name           = "snowflake-transform"
    type           = "snowflake_sql_query"
    connection_id  = boomi_data_integration_connection.snowflake.id
    sql_query      = var.sql_query
    is_enabled     = true
    disable_errors = false
    target_settings = {
      target_type   = "table" # discriminator the API requires on the target union
      database_name = var.sf_target_database
      schema_name   = var.sf_target_schema
      table_name    = var.sf_target_table
      loading_mode  = "overwrite"
    }
  }

  # Assemble the ordered pipeline: river -> (python) -> snowflake SQL.
  steps = concat([local.river_step], local.python_steps, [local.sql_step])
}

resource "boomi_data_integration_data_flow" "logic" {
  environment_id = var.environment_id
  name           = var.river_name
  description    = "Multi-step logic river (run sub-river -> optional python -> Snowflake SQL transform), managed by terraform-provider-data-integration"
  type           = "logic"
  kind           = "main_river"

  # Route the shared Snowflake connection through the env's group (see note above).
  group_id = var.group_id

  settings_json = jsonencode({})

  properties_json = jsonencode({
    properties_type = "logic"
    logic_steps = [
      {
        name           = "pipeline"
        type           = "run_once"
        is_parallel    = false
        is_enabled     = true
        disable_errors = false
        steps          = local.steps
      }
    ]
  })
}

output "logic_river_id" {
  description = "cross_id of the created logic river."
  value       = boomi_data_integration_data_flow.logic.id
}

output "snowflake_connection_id" {
  value = boomi_data_integration_connection.snowflake.id
}

output "step_count" {
  description = "Number of steps in the pipeline container (2 without a Python step, 3 with)."
  value       = length(local.steps)
}
