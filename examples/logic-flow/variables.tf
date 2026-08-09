variable "environment_id" {
  type        = string
  description = "An EXISTING environment ID. This example does not create an environment."
}

variable "group_id" {
  type        = string
  description = "Group cross_id used to route the shared Snowflake connection. Required for the SQL step to run (Trap: unset group -> misleading connection/404 error at run time)."
  default     = null
}

# ── Logic data flow ─────────────────────────────────────────────────────────
variable "data_flow_name" {
  type        = string
  description = "Name for the logic data flow."
  default     = "TF_logic_multistep"
}

variable "sub_data_flow_id" {
  type        = string
  description = "cross_id of an existing data flow to run as the first (river) step."
}

# ── Python (logicode) step — optional ──────────────────────────────────────────
variable "include_python_step" {
  type        = bool
  description = "Create and wire in the Python (logicode) step. false = 2-step pipeline without it."
  default     = true
}

variable "python_size" {
  type        = string
  description = "Logicode compute size (XS, S, M, L, XL, XXL)."
  default     = "XS"

  validation {
    condition     = contains(["XS", "S", "M", "L", "XL", "XXL"], var.python_size)
    error_message = "python_size must be one of XS, S, M, L, XL, XXL."
  }
}

variable "python_packages" {
  type        = list(string)
  description = "Extra pip packages available to the Python step."
  default     = []
}

# ── Snowflake SQL / DB transformation step ──────────────────────────────────────
variable "snowflake_connection_name" {
  type    = string
  default = "tf_logic_snowflake"
}

variable "sf_account_name" {
  type        = string
  description = "Snowflake account identifier (e.g. xy12345.us-east-1)."
}
variable "sf_username" {
  type = string
}
variable "sf_password" {
  type      = string
  sensitive = true
}
variable "sf_warehouse" {
  type    = string
  default = "COMPUTE_WH"
}
variable "sf_role" {
  type    = string
  default = "SYSADMIN"
}

variable "sql_query" {
  type        = string
  description = "SQL the transformation step runs; its result is written to the target table."
  default     = "SELECT 1 AS id, 'hello' AS msg"
}

variable "sf_target_database" {
  type = string
}
variable "sf_target_schema" {
  type    = string
  default = "PUBLIC"
}
variable "sf_target_table" {
  type    = string
  default = "TF_LOGIC_TRANSFORM"
}
