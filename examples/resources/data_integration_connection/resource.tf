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

# MSSQL — with SSH tunnel and key file
resource "boomi_data_integration_connection" "mssql_with_ssh" {
  name           = "mssql_with_ssh"
  type           = "mssql"
  environment_id = var.environment_id

  parameters_json = jsonencode({
    host            = "<YOUR_MSSQL_HOST>"
    port            = 1433
    database        = "<YOUR_DATABASE>"
    username        = "<YOUR_USERNAME>"
    password        = "<YOUR_PASSWORD>"
    is_ssh_tunnel   = true
    ssh_remote_host = "<YOUR_SSH_TUNNEL_HOST>"
    ssh_remote_port = 22
    ssh_remote_user = "<YOUR_SSH_TUNNEL_USER>"
  })

  file_params = {
    ssh_pkey_file_path = "<LOCAL_PATH_TO_KEY.pem>"
  }
}

# MSSQL — direct connection (no SSH)
resource "boomi_data_integration_connection" "mssql" {
  name           = "mssql"
  type           = "mssql"
  environment_id = var.environment_id

  parameters_json = jsonencode({
    host     = "<YOUR_MSSQL_HOST>"
    port     = 1433
    database = "<YOUR_DATABASE>"
    username = "<YOUR_USERNAME>"
    password = "<YOUR_PASSWORD>"
  })
}
