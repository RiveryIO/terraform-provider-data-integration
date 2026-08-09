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
