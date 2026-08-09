# ── Required ──────────────────────────────────────────────────────────────────

variable "jira_connection_id" {
  type        = string
  description = "ID of an existing boomi_data_integration_connection resource with type = \"jira\"."
}

variable "snowflake_connection_id" {
  type        = string
  description = "ID of an existing boomi_data_integration_connection resource with type = \"snowflake\"."
}

# ── Identity ──────────────────────────────────────────────────────────────────

variable "name" {
  type        = string
  description = "Name of the data flow as it appears in the UI."
  default     = "Jira → Snowflake"
}

variable "activate" {
  type        = bool
  description = "Whether to activate the data flow immediately after creation."
  default     = false
}

# ── Jira source ───────────────────────────────────────────────────────────────

variable "jira_report" {
  type        = string
  description = "Report to extract. Common values: issue (default), project, field, user, sprint, board."
  default     = "issue"
}

variable "keep_raw_customfields" {
  type        = bool
  description = "Include raw Jira custom field data alongside mapped columns."
  default     = false
}

variable "time_period" {
  type        = string
  description = <<-EOT
    Extraction window. Named windows: yesterday, last_7_days, last_30_days,
    week_to_date, month_to_date, year_to_date, etc.
    Use "date_range" together with var.start_date for an explicit range.
  EOT
  default     = "last_week"
}

variable "start_date" {
  type        = string
  description = "Inclusive start date when time_period = \"date_range\". Format: \"YYYY-MM-DD HH:MM:SS\" (UTC)."
  default     = null
}

variable "utc_offset" {
  type        = number
  description = "UTC offset in hours applied when computing time windows."
  default     = 0
}

# ── Snowflake target ──────────────────────────────────────────────────────────

variable "loading_method" {
  type        = string
  description = "How to load into Snowflake: overwrite | append | merge."
  default     = "overwrite"
}

variable "target_database" {
  type        = string
  description = "Snowflake database to load into."
}

variable "target_schema" {
  type        = string
  description = "Snowflake schema to load into."
  default     = "public"
}

variable "target_table" {
  type        = string
  description = "Destination table name in Snowflake."
  default     = "jira_issues"
}

# ── Column mapping ────────────────────────────────────────────────────────────
# Override to add/remove columns or change types.
# Set to null to let the connector auto-detect the schema on first run.
# The built-in default (local.default_issue_columns in columns.tf) covers
# the standard Jira "issue" report layout (147 columns).

variable "columns" {
  type        = list(any)
  description = "Column mapping for the Snowflake target table. Defaults to the standard 147-column Jira issue layout (see columns.tf)."
  default     = null
}
