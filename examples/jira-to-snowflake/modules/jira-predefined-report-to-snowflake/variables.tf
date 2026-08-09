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
  default     = "Jira Predefined Reports → Snowflake"
}

variable "activate" {
  type        = bool
  description = "Whether to activate the data flow immediately after creation."
  default     = false
}

# ── Reports ───────────────────────────────────────────────────────────────────

variable "reports" {
  description = <<-EOT
    List of Jira predefined reports to sync. Each entry maps one Jira report to
    one Snowflake table.

    Available report_names: project, user, sprint, board, epic,
    sprint_velocity_report, epic_report, version_report,
    control_chart, cumulative_flow_diagram.

    time_period accepts named windows (last_7_days, month_to_date, year_to_date …)
    or "date_range" — set start_date when using date_range.
  EOT
  type = list(object({
    report_name  = string
    target_table = string
    time_period  = optional(string, "date_range")
    start_date   = optional(string, "2024-01-01 00:00:00")
    last_days    = optional(number, 3)
  }))
  default = [
    {
      report_name  = "user"
      target_table = "jira_user"
      time_period  = "date_range"
      start_date   = "2024-01-01 00:00:00"
      last_days    = 3
    }
  ]
}

# ── Jira source ───────────────────────────────────────────────────────────────

variable "utc_offset" {
  type        = number
  description = "UTC offset in hours applied when computing time windows."
  default     = 0
}

# ── Snowflake target ──────────────────────────────────────────────────────────

variable "target_database" {
  type        = string
  description = "Snowflake database to load into."
}

variable "target_schema" {
  type        = string
  description = "Snowflake schema to load into."
  default     = "public"
}

variable "target_table_prefix" {
  type        = string
  description = "Optional prefix prepended to every table name in Snowflake."
  default     = ""
}
