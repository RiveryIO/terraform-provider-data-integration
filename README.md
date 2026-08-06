# terraform-provider-data-integration

Terraform provider for **Boomi Data Integration**. Manage connections, data flows,
and environments as code.

**[Documentation on the Terraform Registry →](https://registry.terraform.io/providers/riveryio/data-integration/latest/docs)**

## Examples

Runnable configurations are in [`examples/`](examples/):

| Example | What it shows |
|---|---|
| [`data-flow-basic/`](examples/data-flow-basic/) | Minimal Jira → Snowflake flow |
| [`jira-to-snowflake/`](examples/jira-to-snowflake/) | Full load vs. rolling-window report |
| [`cdc/`](examples/cdc/) | MySQL CDC — snapshot-then-stream and stream-only |
| [`mysql-incremental-to-snowflake/`](examples/mysql-incremental-to-snowflake/) | Incremental extraction |
| [`logic-flow/`](examples/logic-flow/) | Orchestration: chain flows and SQL steps |
| [`connection-jira/`](examples/connection-jira/) | Jira connection |
| [`connection-snowflake/`](examples/connection-snowflake/) | Snowflake connection |
| [`connection-s3/`](examples/connection-s3/) | S3 file zone connection |
