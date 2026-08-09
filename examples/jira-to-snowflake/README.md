# Jira → Snowflake

Creates two Jira data flows in one apply:

| Flow | `run_type` | What it does |
|---|---|---|
| `jira_regular` | `regular` | Fetches the Jira "issue" report on each run, writes all rows to a single Snowflake table. Column schema is pinned to the standard 147-column layout. |
| `jira_predefined` | `predefined_report` | Runs one or more Jira built-in reports (project, user, sprint, …). Each report writes to its own Snowflake table. |

## Quick start

```bash
cp terraform.tfvars.example terraform.tfvars
# fill in credentials
terraform init
terraform plan
terraform apply
```

Credentials can also be supplied via environment variables instead of `terraform.tfvars`:

```bash
export DATA_INTEGRATION_API_TOKEN="..."
export DATA_INTEGRATION_ACCOUNT_ID="..."
export DATA_INTEGRATION_ENVIRONMENT_ID="..."
```

## Module structure

```
main.tf                                       # connections + two module calls
variables.tf
modules/
  jira-to-snowflake/                          # regular flow wrapper
    main.tf                                   # → github.com/RiveryIO/terraform-data-integration-dataflow
    variables.tf                              # includes 147-column default schema
  jira-predefined-report-to-snowflake/        # predefined_report wrapper
    main.tf                                   # → github.com/RiveryIO/terraform-data-integration-dataflow
    variables.tf
```

## Adding more predefined reports

Extend the `reports` list in `main.tf`:

```hcl
reports = [
  { report_name = "project", target_table = "jira_project", time_period = "date_range", start_date = "2024-01-01 00:00:00", last_days = 3 },
  { report_name = "user",    target_table = "jira_user",    time_period = "date_range", start_date = "2024-01-01 00:00:00", last_days = 3 },
  { report_name = "sprint",  target_table = "jira_sprint",  time_period = "last_7_days", last_days = 7 },
]
```

Available report names: `project`, `user`, `sprint`, `board`, `epic`,
`sprint_velocity_report`, `epic_report`, `version_report`,
`control_chart`, `cumulative_flow_diagram`.
