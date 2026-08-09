# ── Provider / API ────────────────────────────────────────────────────────────

variable "api_url" {
  type        = string
  description = "Base URL of the Data Integration API."
  default     = null
}

variable "api_token" {
  type        = string
  description = "Data Integration API token. Prefer the DATA_INTEGRATION_API_TOKEN env var."
  sensitive   = true
  default     = null
}

variable "account_id" {
  type        = string
  description = "Data Integration account ID."
  default     = null
}

variable "environment_id" {
  type        = string
  description = "An existing environment ID. This example does not create one."
}

# ── Jira source ───────────────────────────────────────────────────────────────

variable "jira_base_url" {
  type        = string
  description = "Jira instance URL, e.g. https://yourorg.atlassian.net"
}

variable "jira_username" {
  type        = string
  description = "Jira username (email address for Atlassian Cloud)."
}

variable "jira_api_token" {
  type        = string
  sensitive   = true
  description = "Atlassian API token for the Jira user."
}

# ── Snowflake target ──────────────────────────────────────────────────────────

variable "snowflake_account" {
  type        = string
  description = "Snowflake account identifier, e.g. xy12345.us-east-1."
}

variable "snowflake_username" {
  type        = string
  description = "Snowflake username."
}

variable "snowflake_password" {
  type        = string
  sensitive   = true
  description = "Snowflake password."
}

variable "snowflake_warehouse" {
  type        = string
  description = "Snowflake warehouse name."
}

variable "snowflake_database" {
  type        = string
  description = "Snowflake database to load into."
}

variable "snowflake_schema" {
  type        = string
  description = "Snowflake schema to load into."
  default     = "PUBLIC"
}

variable "snowflake_role" {
  type        = string
  description = "Snowflake role to assume."
  default     = null
}

# ── Activation ────────────────────────────────────────────────────────────────

variable "activate" {
  type        = bool
  description = "Activate both data flows after creation."
  default     = false
}
