# Multi-step logic data flow authored with the riveryio/data-integration provider.
#
# A logic data flow orchestrates other steps in order. This example builds a
# `run_once` container with up to three step types:
#
#   1. river                — run an existing data flow (e.g. the source-to-target data flow)
#   2. logicode (python)    — run a Python step  [optional; see the file_id note below]
#   3. <warehouse>_sql_query — a SQL / DB transformation writing to a warehouse table
#
# The data flow body is opaque JSON (`properties_json`), so this config emits the
# exact step shapes the Data Integration API accepts (matching logic_builder.py).
#
# NOTE on the Python step: `boomi_data_integration_logicode_file` uploads the
# code itself — set include_python_step = true (the default) to create it and
# wire it in as the second step.
#
# NOTE on group_id: logic data flows that use a shared warehouse connection (the
# Snowflake SQL step below) must set `group_id` to the environment's group
# cross_id, or the connection fails to route at run time (misleading 404). The
# provider exposes `group_id` on the data flow for exactly this reason.

# Snowflake connection used by the SQL / DB transformation step.
resource "boomi_data_integration_connection" "snowflake" {
  environment_id = var.environment_id
  name           = var.snowflake_connection_name
  type           = "snowflake"

  parameters_json = jsonencode({
    account_name          = var.sf_account_name
    username              = var.sf_username
    password              = var.sf_password
    warehouse             = var.sf_warehouse
    role                  = var.sf_role
    default_database_name = var.sf_target_database
  })
}

# The Python step's code, uploaded to the platform as its own resource — its
# id is what a logicode step references, not the code itself.
resource "boomi_data_integration_logicode_file" "transform" {
  count    = var.include_python_step ? 1 : 0
  filename = "transform.py"
  content  = file("${path.module}/transform.py")
}

locals {
  # Step 1: run the existing (source-to-target) data flow.
  data_flow_step = {
    name            = "run-s2t-subflow"
    type            = "river"
    river_id        = var.sub_data_flow_id
    input_variables = {}
    is_enabled      = true
    disable_errors  = false
  }

  # Step 2 (optional): a Python (logicode) step.
  python_steps = var.include_python_step ? [{
    name                = "python-transform"
    type                = "logicode"
    code_type           = "python"
    file_id             = boomi_data_integration_logicode_file.transform[0].id
    logicode_size       = var.python_size
    additional_packages = var.python_packages
    is_enabled          = true
    disable_errors      = false
  }] : []

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

  # Assemble the ordered pipeline: data flow -> (python) -> snowflake SQL.
  steps = concat([local.data_flow_step], local.python_steps, [local.sql_step])
}

resource "boomi_data_integration_data_flow" "logic" {
  environment_id = var.environment_id
  name           = var.data_flow_name
  description    = "Multi-step logic data flow (run sub-data-flow -> optional python -> Snowflake SQL transform), managed by terraform-provider-data-integration"
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

output "logic_data_flow_id" {
  description = "cross_id of the created logic data flow."
  value       = boomi_data_integration_data_flow.logic.id
}

output "snowflake_connection_id" {
  value = boomi_data_integration_connection.snowflake.id
}

output "step_count" {
  description = "Number of steps in the pipeline container (2 without a Python step, 3 with)."
  value       = length(local.steps)
}

output "python_file_id" {
  description = "cross_id of the uploaded logicode file, when include_python_step is true."
  value       = var.include_python_step ? boomi_data_integration_logicode_file.transform[0].id : null
}
