# A source-to-target data flow (the API calls this a "river") moves tables from
# a source connection into a target connection. Unlike a logic data flow (see
# ../main.tf), its type is "source_to_target" and its properties describe a
# source, a target, and the schemas/tables to move. The provider passes
# properties_json through to the API verbatim, so this file emits the API body
# shape directly.

# --- Source connection (MySQL) ---
resource "boomi_data_integration_connection" "source" {
  environment_id = var.environment_id
  name           = "example-mysql-source"
  type           = "mysql"

  parameters_json = jsonencode({
    host     = var.mysql_host
    port     = 3306
    username = var.mysql_username
    password = var.mysql_password
    database = var.mysql_database
    ssl_mode = "disabled"
  })
}

# --- Target connection (PostgreSQL) ---
# The Postgres target stages the load through S3 (bulk COPY), so the connection
# also carries S3 credentials. Without them the connection test fails.
resource "boomi_data_integration_connection" "target" {
  environment_id = var.environment_id
  name           = "example-postgres-target"
  type           = "postgres"

  parameters_json = jsonencode({
    host              = var.postgres_host
    port              = 5432
    username          = var.postgres_username
    password          = var.postgres_password
    database          = var.postgres_database
    ssl_mode          = "disabled"
    default_bucket    = var.s3_bucket
    aws_access_key    = var.s3_access_key
    aws_access_secret = var.s3_access_secret
    region            = var.s3_region
  })
}

# --- The source-to-target data flow ---
# NB: the API's Postgres target name is "postgres_rds", not "postgres".
resource "boomi_data_integration_data_flow" "mysql_to_postgres" {
  environment_id = var.environment_id
  name           = "example-mysql-to-postgres"
  type           = "source_to_target"
  kind           = "main_river"
  description    = "Example MySQL -> PostgreSQL source-to-target data flow"

  # Typed settings block (replaces the deprecated settings_json — setting both
  # is a configuration error).
  settings = {
    run_timeout_seconds = 180
    notification = {
      failure = {
        email      = "data-oncall@example.com"
        is_enabled = true
      }
    }
  }

  # Typed schedule block (replaces the deprecated schedulers_json). Singular:
  # the API permits at most one scheduler. Mandatory and must be enabled for CDC
  # (log-based) flows, where the cron must fire between once per day and 12
  # times per hour.
  #
  # schedule = {
  #   cron_expression = "0 * * * *"
  #   is_enabled      = true
  # }

  properties_json = jsonencode({
    source = {
      name          = "mysql"
      run_type      = "multi_tables"
      connection_id = boomi_data_integration_connection.source.id
    }
    target = {
      name           = "postgres_rds"
      connection_id  = boomi_data_integration_connection.target.id
      loading_method = "overwrite"
      database_name  = var.postgres_database
      schema_name    = "public"
    }
    schemas = [{
      name = var.mysql_database
      tables = [
        { details = { name = "customers", is_selected = true, target_table = "customers", extract_method = "all" } },
        { details = { name = "orders", is_selected = true, target_table = "orders", extract_method = "all" } },
      ]
    }]
  })
}

variable "environment_id" {
  type        = string
  description = "Existing environment id to create the connections + data flow in."
}

variable "mysql_host" {
  type = string
}
variable "mysql_username" {
  type = string
}
variable "mysql_password" {
  type      = string
  sensitive = true
  default   = ""
}
variable "mysql_database" {
  type = string
}

variable "postgres_host" {
  type = string
}
variable "postgres_username" {
  type = string
}
variable "postgres_password" {
  type      = string
  sensitive = true
  default   = ""
}
variable "postgres_database" {
  type = string
}

variable "s3_bucket" {
  type    = string
  default = ""
}
variable "s3_access_key" {
  type      = string
  sensitive = true
  default   = ""
}
variable "s3_access_secret" {
  type      = string
  sensitive = true
  default   = ""
}
variable "s3_region" {
  type    = string
  default = "us-east-2"
}

output "data_flow_id" {
  value = boomi_data_integration_data_flow.mysql_to_postgres.id
}
