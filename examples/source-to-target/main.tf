terraform {
  required_providers {
    boomi = {
      source = "riveryio/data-integration"
    }
  }
}

# Credentials come from DATA_INTEGRATION_API_TOKEN / _ACCOUNT_ID / _API_URL,
# or set them inline (token is sensitive — prefer the environment).
provider "boomi" {
  # api_url        = "http://localhost:8008"          # devbox api-service
  # environment_id = var.environment_id
}

# A source-to-target river ("data flow") moves tables from a source connection
# into a target connection. Unlike a logic river (see ../main.tf), its type is
# "source_to_target" and its properties describe a source, a target, and the
# schemas/tables to move. The provider passes properties_json through to the API
# verbatim, so this file emits the API body shape directly.
#
# Verified end-to-end on a devbox (MySQL gold DB -> PostgreSQL): 75 rows across
# four tables.

# --- Source connection (MySQL) ---
resource "boomi_connection" "source" {
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
resource "boomi_connection" "target" {
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

# --- The source-to-target river ---
# NB: the API's Postgres target name is "postgres_rds", not "postgres".
resource "boomi_data_flow" "mysql_to_postgres" {
  environment_id = var.environment_id
  name           = "example-mysql-to-postgres"
  type           = "source_to_target"
  kind           = "main_river"
  description    = "Example MySQL -> PostgreSQL source-to-target river"

  settings_json = jsonencode({ run_timeout_seconds = 180 })

  properties_json = jsonencode({
    source = {
      name          = "mysql"
      run_type      = "multi_tables"
      connection_id = boomi_connection.source.id
    }
    target = {
      name           = "postgres_rds"
      connection_id  = boomi_connection.target.id
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
  description = "Existing environment id to create the connections + river in."
}

variable "mysql_host" {
  type    = string
  default = "dx-mysql"
}
variable "mysql_username" {
  type    = string
  default = "rivery"
}
variable "mysql_password" {
  type      = string
  sensitive = true
  default   = ""
}
variable "mysql_database" {
  type    = string
  default = "rivery_dev"
}

variable "postgres_host" {
  type    = string
  default = "dx-postgres"
}
variable "postgres_username" {
  type    = string
  default = "rivery"
}
variable "postgres_password" {
  type      = string
  sensitive = true
  default   = ""
}
variable "postgres_database" {
  type    = string
  default = "rivery_dev"
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

output "river_id" {
  value = boomi_data_flow.mysql_to_postgres.id
}
