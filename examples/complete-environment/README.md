# complete-environment

Jira + Snowflake + S3 in one `terraform apply`. A concrete starting point — edit
the placeholder values in `main.tf`, then run it.

## 1. Configure the provider

Credentials are read from environment variables so nothing sensitive goes in `.tf` files:

```bash
export DATA_INTEGRATION_API_TOKEN="your-api-token"
export DATA_INTEGRATION_ACCOUNT_ID="your-account-id"
export DATA_INTEGRATION_ENVIRONMENT_ID="your-env-id"
```

Find them in the Boomi Data Integration UI:
- **token** → Settings → API Tokens → Generate
- **account_id** → Settings → Account → Account ID
- **environment_id** → Environments page → click the environment → ID in the URL

## 2. Connections

A connection authenticates to one system. Replace the placeholder values in
`main.tf` with your own:

```hcl
resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  parameters_json = jsonencode({
    base_url = "https://yourorg.atlassian.net"
    username = "user@example.com"
    password = "..."   # Atlassian API token — Settings → Security → API Tokens
  })
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account   = "xy12345.us-east-1"
    username  = "SVC_USER"
    password  = "..."
    database  = "ANALYTICS"
    warehouse = "COMPUTE_WH"
    schema    = "PUBLIC"
  })
}

resource "boomi_data_integration_connection" "s3" {
  name = "S3 File Zone"
  type = "aws_fz"

  parameters_json = jsonencode({
    aws_access_key    = "AKIA..."
    aws_access_secret = "..."
    region            = "us-east-1"
    bucket_name       = "my-data-bucket"
  })
}
```

`parameters_json` is **write-only** — the API never returns credentials on read,
so rotating a secret is just editing the value and re-running `terraform apply`.

## 3. Data flows

### Full load: Jira Issues → Snowflake

Pulls every Jira issue into a Snowflake table on each run:

```hcl
resource "boomi_data_integration_data_flow" "jira_issues" {
  name     = "Jira Issues → Snowflake"
  kind     = "main_river"
  type     = "source_to_target"
  activate = false   # flip to true when ready to schedule

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      run_type      = "single_table"
      cdc_settings  = null
      additional_settings = { source_type = "source_to_target" }
    }
    target = {
      name          = "snowflake"
      connection_id = boomi_data_integration_connection.snowflake.id
      schema        = "PUBLIC"
      db            = "ANALYTICS"
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "single_table"
        details = {
          name                       = "issues"
          target_table               = "jira_issues"
          is_selected                = true
          is_custom_incremental      = false
          exporter_chunk_size        = 30000
          additional_source_settings = { report_type = "full_table" }
          # required null fields
          modified_columns = []; incremental_field = null; date_range = null
          running_number = null; epoch = null; change_tracking_settings = null
          system_versioning_settings = null; additional_target_settings = null
          cdc_settings = { initiate_table = null, overwrite_table_in_migration = null }
        }
      }]
    }]
  })
}
```

### Rolling export: Jira Issues (7 days) → S3

Exports the last 7 days of issues as CSV on each run:

```hcl
resource "boomi_data_integration_data_flow" "jira_weekly_export" {
  name = "Jira Issues (7 days) → S3"
  kind = "main_river"
  type = "source_to_target"

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "jira"
      connection_id = boomi_data_integration_connection.jira.id
      run_type      = "single_table"
      cdc_settings  = null
      additional_settings = { source_type = "source_to_target" }
    }
    target = {
      name          = "aws_fz"
      connection_id = boomi_data_integration_connection.s3.id
      bucket_name   = "my-data-bucket"
      fz_partition  = "d"
      delimiter     = ","
      source_format = "CSV"
    }
    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "single_table"
        details = {
          name         = "issues"
          target_table = "jira_issues_weekly"
          is_selected  = true
          additional_source_settings = { report_type = "predefined", time_period = "last_7_days" }
          # ...
        }
      }]
    }]
  })
}
```

The key difference is `additional_source_settings`:
- `report_type = "full_table"` — all records every run
- `report_type = "predefined"` + `time_period = "last_7_days"` — rolling window

## 4. Apply

```bash
terraform init
terraform plan
terraform apply
```

## What to do next

**Add a schedule** — add `settings_json` to run the flow automatically:

```hcl
settings_json = jsonencode({
  run_timeout_seconds = 3600
  notification = {
    failure = { email = "alerts@example.com", is_enabled = true, execution_time_limit_seconds = null }
    warning = { email = "alerts@example.com", is_enabled = false, execution_time_limit_seconds = null }
    run_threshold = { email = "alerts@example.com", is_enabled = false, execution_time_limit_seconds = 0 }
  }
})
```

**Add more tables** — extend the `schemas[].tables` array with additional entries.

**Real-time replication** — see [`../cdc/`](../cdc/) for MySQL CDC with snapshot + streaming.

**Orchestration** — see [`../logic-flow/`](../logic-flow/) to chain flows and SQL steps.
