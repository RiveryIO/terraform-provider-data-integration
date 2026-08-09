# Jira → Snowflake example

Creates two data flows from Jira into Snowflake using local modules that wrap the
[`terraform-data-integration-dataflow`](https://github.com/RiveryIO/terraform-data-integration-dataflow) module.

| Flow | Module | What it does |
|---|---|---|
| `jira_regular` | `modules/jira-to-snowflake` | Syncs the Jira `issue` report into one table (regular run, 147-column schema) |
| `jira_predefined` | `modules/jira-predefined-report-to-snowflake` | Syncs one or more Jira built-in reports (`user`, `sprint`, …) into separate tables |

## Usage

```bash
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars with your credentials

export DATA_INTEGRATION_API_TOKEN="..."
export DATA_INTEGRATION_ACCOUNT_ID="..."
export DATA_INTEGRATION_ENVIRONMENT_ID="..."

terraform init
terraform apply
```

## Customising the column schema

The `jira_regular` flow pins the Jira issue schema to 147 columns by default
(defined in `modules/jira-to-snowflake/columns.tf`). To override:

```hcl
module "jira_regular" {
  source = "./modules/jira-to-snowflake"
  # ...
  columns = [
    { name = "id",  type = "STRING",  mode = "NULLABLE", alias = "id",  fieldName = "id",  id = "id",  fields = [] },
    { name = "key", type = "STRING",  mode = "NULLABLE", alias = "key", fieldName = "key", id = "key", fields = [] },
    # ...
  ]
}
```

Set `columns = null` to let the connector auto-detect the schema on the first run.
