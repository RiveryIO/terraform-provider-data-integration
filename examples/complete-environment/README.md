# Example: Complete environment (onboarding starter)

A self-contained Terraform configuration for teams getting started with Boomi
Data Integration as code. Creates three connections (Jira, Snowflake, S3) and
two data flows in a single environment.

Use this as a fork-and-fill-in template — replace the variable values in
`terraform.tfvars` and `terraform apply`.

## What it creates

| Resource | Description |
|---|---|
| `boomi_data_integration_connection.jira` | Jira Cloud source |
| `boomi_data_integration_connection.snowflake` | Snowflake data warehouse target |
| `boomi_data_integration_connection.s3` | S3 file zone target |
| `boomi_data_integration_data_flow.jira_issues` | Full Jira issues load → Snowflake |
| `boomi_data_integration_data_flow.jira_weekly_export` | Rolling 7-day Jira export → S3 CSV |

## Getting started

```bash
# 1. Copy and fill in credentials
cp terraform.tfvars.example terraform.tfvars
$EDITOR terraform.tfvars

# 2. Initialise the provider
terraform init

# 3. Preview what will be created
terraform plan

# 4. Apply
terraform apply
```

After apply, set `activate = true` on each data flow resource (or activate from
the UI) to start scheduling runs.

## Where to find your IDs

| Value | Location in UI |
|---|---|
| `api_token` | Settings → API Tokens → Generate |
| `account_id` | Settings → Account → Account ID |
| `environment_id` | Environments page → click the environment → copy the ID from the URL |

## Next steps

- Add a scheduler (`settings_json`) to run the flow automatically
- Add more tables to the `schemas` array
- Try the [CDC example](../cdc/) for real-time replication
- Try the [logic-flow example](../logic-flow/) to orchestrate multiple flows
