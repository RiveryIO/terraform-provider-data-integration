# terraform-provider-data-integration

Terraform provider for **Boomi Data Integration**. Declare environments,
connections, and data flows in `.tf`, plan the diff, and apply through the
Data Integration API.

## Getting started

```hcl
terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 1.0"
    }
  }
}

provider "boomi" {
  api_url        = "https://api.rivery.io"
  token          = var.api_token
  account_id     = var.account_id
  environment_id = var.environment_id
}
```

See the **[Getting Started guide](docs/guides/getting-started.md)** for a
step-by-step walkthrough.

The **[`examples/complete-environment/`](examples/complete-environment/)** directory
is the recommended starting point: a fork-and-fill template that creates
connections and data flows in a single `terraform apply`.

## Examples

| Example | What it shows |
|---|---|
| [`complete-environment/`](examples/complete-environment/) | Onboarding starter — Jira + Snowflake + S3, two flows |
| [`source-to-target/`](examples/source-to-target/) | MySQL → PostgreSQL source-to-target |
| [`source-to-target-bigquery/`](examples/source-to-target-bigquery/) | BigQuery → Snowflake with explicit schema |
| [`jira-to-snowflake/`](examples/jira-to-snowflake/) | Jira issues: full load + rolling-window report |
| [`cdc/`](examples/cdc/) | MySQL CDC: migrate-then-stream + stream-only variants |
| [`mysql-incremental-to-snowflake/`](examples/mysql-incremental-to-snowflake/) | MySQL incremental extraction — discovery-driven and hand-written |
| [`logic-flow/`](examples/logic-flow/) | Logic flow orchestrating S2T + SQL steps |
| [`connection-discovery/`](examples/connection-discovery/) | Discover available connection types |
| [`type-discovery/`](examples/type-discovery/) | Discover available source/target types |
| [`data-flow-run/`](examples/data-flow-run/) | Trigger a data flow run from Terraform |

## Resources

| Resource | Scope | Import |
|---|---|---|
| `boomi_data_integration_environment` | account | `<id>` |
| `boomi_data_integration_connection` | env | `<env_id>/<id>` |
| `boomi_data_integration_data_flow` | env | `<env_id>/<id>` |
| `boomi_data_integration_blueprint` | env | `<env_id>/<id>` |
| `boomi_data_integration_variable` | env | `<env_id>/<key>` |
| `boomi_data_integration_data_flow_cdc_config` | env | `<env_id>/<id>` |

All resources support `import`, drift detection via `Read`, and force-replace
on immutable fields (`environment_id`, connection `type`).

## Data sources

| Data source | Purpose |
|---|---|
| `boomi_data_integration_connection_type` | Look up a single connection type by name |
| `boomi_data_integration_connection_types` | List all available connection types |
| `boomi_data_integration_source_types` | List source types for data flows |
| `boomi_data_integration_target_types` | List target types for data flows |
| `boomi_data_integration_source_metadata` | Introspect a live source for table schemas |

## Authentication

Credentials resolve from provider attributes or environment variables (attribute wins):

| Attribute | Env var | Required |
|---|---|---|
| `token` | `DATA_INTEGRATION_API_TOKEN` | yes |
| `account_id` | `DATA_INTEGRATION_ACCOUNT_ID` | yes |
| `api_url` | `DATA_INTEGRATION_API_URL` | no (default: `https://api.rivery.io`) |
| `environment_id` | `DATA_INTEGRATION_ENVIRONMENT_ID` | no |

**Where to find your credentials:**
- `token` — **Settings → API Tokens → Generate** in the Data Integration UI
- `account_id` — **Settings → Account → Account ID**
- `environment_id` — **Environments page** → click the environment → copy the ID from the URL

## Guides

Detailed reference guides are published to the Terraform Registry alongside this provider:

- [Getting Started](docs/guides/getting-started.md) — from zero to a running data flow
- [CDC Data Flows](docs/guides/cdc-data-flows.md) — snapshot + streaming CDC
- [Blueprint Data Flows](docs/guides/blueprint-data-flows.md) — recipe-driven parameterised flows
- [Logic Data Flows](docs/guides/logic-data-flows.md) — orchestration flows
- [Incremental Extraction](docs/guides/incremental-extraction.md) — date-range and running-number increments
- [Source-to-Target (API Connectors)](docs/guides/source-to-target-api-connectors.md)
- [Source-to-Target (Databases)](docs/guides/source-to-target-databases.md)

## Develop

```bash
make build       # build ./bin/terraform-provider-data-integration
make test        # unit tests (acceptance tests auto-skip without TF_ACC)
make fmt vet     # format + vet
make docs        # regenerate docs/ from templates/
```

Dev override (run examples against a local build without publishing):

```bash
cat > dev.tfrc <<EOF
provider_installation {
  dev_overrides { "riveryio/data-integration" = "$(pwd)/bin" }
  direct {}
}
EOF
make build
cd examples/complete-environment && TF_CLI_CONFIG_FILE=../../dev.tfrc terraform plan
```

## Acceptance tests

```bash
export DATA_INTEGRATION_API_TOKEN=...
export DATA_INTEGRATION_ACCOUNT_ID=...
export DATA_INTEGRATION_ENVIRONMENT_ID=...
make testacc
```

## Layout

```
main.go                    provider entrypoint
internal/client/           Data Integration API client
internal/provider/         provider + resource implementations
examples/                  runnable example configurations
templates/                 tfplugindocs templates + guides (SOURCE — edit these, not docs/)
docs/                      Terraform Registry docs (GENERATED by make docs)
```
