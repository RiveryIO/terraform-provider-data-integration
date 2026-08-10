variable "environment_id" {
  type        = string
  description = "An EXISTING environment ID. This example does not create one."
}

variable "group_id" {
  type        = string
  description = "Group cross_id for the data flows (the API assigns the environment default when unset)."
  default     = null
}

# ── Which route(s) to build ───────────────────────────────────────────────────
variable "create_discovery_driven_flow" {
  type        = bool
  description = "Route 1: discover the mapping from the live MySQL connection via the source_metadata data source."
  default     = true
}

variable "create_hand_written_flow" {
  type        = bool
  description = "Route 2: use the literal per-table mapping in main.tf instead. Both routes can be enabled at once — you get two data flows."
  default     = false
}

# ── Data flow ─────────────────────────────────────────────────────────────────
variable "data_flow_name_prefix" {
  type        = string
  description = "Name prefix; each route appends its own suffix."
  default     = "TF_mysql_incremental_to_snowflake"
}

variable "activate" {
  type        = bool
  description = <<-EOT
    Desired activation state. false authors the flow without scheduling any runs
    — the safe default for an example. Set true only when the connections are
    real and you want the backfill to start.
  EOT
  default     = false
}

variable "run_timeout_seconds" {
  type        = number
  description = "Per-run timeout. The FIRST run is the backfill and is the long one; give it room."
  default     = 43200 # 12h
}

variable "schedule_cron" {
  type        = string
  description = <<-EOT
    5-field UNIX cron driving the forward tracking. Must fire between once per
    day and 12 times per hour (a 5-minute-to-24-hour interval).
  EOT
  default     = "0 * * * *" # hourly
}

variable "schedule_enabled" {
  type        = bool
  description = "Whether the schedule is active."
  default     = false
}

# ── Incremental extraction ────────────────────────────────────────────────────
variable "incremental_field" {
  type        = string
  description = <<-EOT
    The source column that drives the increment for route 1 — applied to EVERY
    discovered table, so all selected tables must have a column by this name.
    Typically an update timestamp (updated_at, modified_on). Use route 2 when
    tables disagree.
  EOT
  default     = "updated_at"
}

variable "backfill_start_date" {
  type        = string
  description = <<-EOT
    RFC3339 timestamp the backfill starts from. Combined with time_period
    "custom" and no end_date, this means "everything from here, then keep going".
  EOT
  default     = "2024-01-01T00:00:00.000+0000"
}

variable "backfill_split_interval" {
  type        = string
  description = <<-EOT
    IntervalTimeExternalEnum: one of "dont_split", "minutes", "hours", "days",
    "weeks", "months", "years". Splits a long backfill into chunks so a single
    request does not have to pull the whole history.
  EOT
  default     = "days"

  validation {
    condition = contains(
      ["dont_split", "minutes", "hours", "days", "weeks", "months", "years"],
      var.backfill_split_interval
    )
    error_message = "Must be a valid IntervalTimeExternalEnum value."
  }
}

variable "backfill_split_interval_size" {
  type        = number
  description = "Number of backfill_split_interval units per chunk."
  default     = 7
}

variable "source_tables" {
  type        = list(string)
  description = "Tables to discover for route 1. Set to null to discover every table in the schema."
  default     = ["customers", "orders"]
}

variable "merge_keys" {
  type        = map(list(string))
  description = <<-EOT
    Source table name → key column names, used to stamp is_key onto the
    discovered columns. merge de-duplicates on these; discovery does not emit
    them. Ignored when snowflake loading_method is not "merge".
  EOT
  default = {
    customers = ["id"]
    orders    = ["order_id"]
  }
}

variable "discovery_timeout" {
  type        = string
  description = "Go duration bounding the metadata-discovery poll. Raise for wide catalogues."
  default     = "8m"
}

# ── MySQL source connection ───────────────────────────────────────────────────
variable "mysql_connection_name" {
  type    = string
  default = "tf-mysql-incremental-source"
}

variable "mysql_host" {
  type        = string
  description = "MySQL hostname."
}

variable "mysql_port" {
  type    = number
  default = 3306
}

variable "mysql_username" {
  type = string
}

variable "mysql_password" {
  type      = string
  sensitive = true
}

variable "mysql_database" {
  type        = string
  description = "MySQL database. For MySQL this doubles as the schema name discovery introspects."
}

variable "mysql_ssl_mode" {
  type    = string
  default = "disabled"
}

# ── Snowflake target connection ───────────────────────────────────────────────
variable "snowflake_connection_name" {
  type    = string
  default = "tf-snowflake-incremental-target"
}

variable "snowflake_account_name" {
  type        = string
  description = "Snowflake account identifier, e.g. \"xy12345.us-east-1\"."
}

variable "snowflake_username" {
  type = string
}

variable "snowflake_password" {
  type      = string
  sensitive = true
}

variable "snowflake_warehouse" {
  type = string
}

variable "snowflake_role" {
  type    = string
  default = null
}

variable "snowflake_database" {
  type        = string
  description = "Target Snowflake database (must already exist)."
}

variable "snowflake_schema" {
  type        = string
  description = "Target Snowflake schema (must already exist)."
  default     = "PUBLIC"
}

variable "snowflake_merge_method" {
  type        = string
  description = <<-EOT
    MergeMethodSnowflake: "merge", "delete_insert", or "switch_tables". Only
    meaningful when the target loading_method is "merge"; the API defaults it to
    "merge" if omitted. (The unrestricted MergeMethod enum also lists
    "insert_on_conflict", but Snowflake does not accept it.)
  EOT
  default     = "merge"

  validation {
    condition     = contains(["merge", "delete_insert", "switch_tables"], var.snowflake_merge_method)
    error_message = "Must be a valid MergeMethodSnowflake value: merge, delete_insert, or switch_tables."
  }
}
