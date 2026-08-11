resource "boomi_data_integration_connection" "example" {
  name = "Snowflake"
  type = "snowflake"

  # Field names come from the live catalog for this connection type:
  #   GET /v1/connections_types/snowflake
  # (or the boomi_data_integration_connection_type data source). They do not
  # match the Snowflake console's own labels — it is account_name, not
  # account, and default_database_name / default_schema_name, not
  # database / schema.
  parameters_json = jsonencode({
    account_name          = "xy12345.us-east-1"
    username              = "SVC_USER"
    password              = "..."
    default_database_name = "ANALYTICS"
    warehouse             = "COMPUTE_WH"
    default_schema_name   = "PUBLIC"
  })
}
