variable "api_url"        { type = string; default = "https://api.rivery.io" }
variable "api_token"      { type = string; sensitive = true }
variable "account_id"     { type = string }
variable "environment_id" { type = string }

variable "jira_base_url"  { type = string; description = "e.g. https://yourorg.atlassian.net" }
variable "jira_username"  { type = string }
variable "jira_api_token" { type = string; sensitive = true }

variable "snowflake_account"   { type = string }
variable "snowflake_username"  { type = string }
variable "snowflake_password"  { type = string; sensitive = true }
variable "snowflake_database"  { type = string }
variable "snowflake_warehouse" { type = string }
variable "snowflake_schema"    { type = string; default = "PUBLIC" }

variable "notification_email" { type = string; default = "" }
