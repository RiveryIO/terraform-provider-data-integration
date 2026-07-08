# ── Provider / API ──────────────────────────────────────────────────────────
variable "api_url" {
  type        = string
  description = "Base URL of the Data Integration API. Devbox = http://localhost:8008 ; Integration = https://api.integration.rivery.in"
}

variable "api_token" {
  type        = string
  description = "Data Integration API token. Prefer the DATA_INTEGRATION_API_TOKEN env var over hardcoding."
  sensitive   = true
  default     = null
}

variable "account_id" {
  type        = string
  description = "Data Integration account ID (or set DATA_INTEGRATION_ACCOUNT_ID)."
  default     = null
}

variable "environment_id" {
  type        = string
  description = "An EXISTING environment ID. This example does not create an environment."
}

# ── Logic river ───────────────────────────────────────────────────────────────
variable "river_name" {
  type        = string
  description = "Name for the logic river (data flow)."
  default     = "TF_logic_orchestration"
}

variable "step_name" {
  type        = string
  description = "Display name for the leaf logic step."
  default     = "run-sub-river"
}

variable "sub_river_id" {
  type        = string
  description = "cross_id of an existing river to run as the logic leaf step (e.g. the source-to-target river)."
}
