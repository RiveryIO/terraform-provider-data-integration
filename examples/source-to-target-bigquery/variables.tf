# ── Provider / API ──────────────────────────────────────────────────────────
variable "api_url" {
  type        = string
  description = "Base URL of the Data Integration API."
}

variable "api_token" {
  type        = string
  description = "Data Integration API token. Prefer the DATA_INTEGRATION_API_TOKEN env var over hardcoding."
  sensitive   = true
  default     = null
}

variable "account_id" {
  type    = string
  default = null
}

variable "environment_id" {
  type        = string
  description = "An EXISTING environment ID. This example does not create one."
}

variable "group_id" {
  type        = string
  description = "Group cross_id for connection routing (API assigns the env default when unset)."
  default     = null
}

# ── Data flow ─────────────────────────────────────────────────────────────────
variable "data_flow_name" {
  type    = string
  default = "TF_s2t_bigquery"
}

# ── BigQuery (gcloud) target connection ───────────────────────────────────────
# NOTE: these are identifiers, not secrets — but do not hardcode real ones; pass
# them via terraform.tfvars (git-ignored). A functional connection also needs a
# service-account KEY FILE uploaded out-of-band (provider gap; see README).
variable "bq_connection_name" {
  type    = string
  default = "tf-bigquery-target"
}

variable "gcp_project_id" {
  type        = string
  description = "GCP project that holds the target BigQuery dataset."
}

variable "gcp_service_account_email" {
  type        = string
  description = "Service-account email for the BigQuery connection."
}

variable "gcp_region" {
  type    = string
  default = "us"
}

variable "bq_dataset_id" {
  type        = string
  description = "Target BigQuery dataset (must already exist in gcp_project_id)."
}
