# Test a connection right after creating it, and fail the plan if it can't connect.
resource "boomi_data_integration_connection" "oracle" {
  environment_id = var.environment_id
  name           = "prod-oracle"
  type           = "oracle"
  parameters_json = jsonencode({
    database_architecture = "single-tenant"
    host                  = "db.example.com"
    port                  = 1521
    database              = "ORCL"
    database_service      = "service_name"
    username              = "app"
    password              = var.oracle_password
    is_ssh_tunnel         = false
  })
}

data "boomi_data_integration_connection_test" "oracle" {
  environment_id = var.environment_id
  connection_id  = boomi_data_integration_connection.oracle.id
  datasource_id  = "oracle"

  lifecycle {
    postcondition {
      condition     = self.success
      error_message = "Oracle connection failed: ${self.error_message}"
    }
  }
}
