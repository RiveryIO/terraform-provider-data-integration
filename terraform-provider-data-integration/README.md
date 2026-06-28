# terraform-provider-data-integration

Terraform provider for **Boomi Data Integration** (Rivery). Declare environments,
connections, and data flows in `.tf`, plan the diff, and apply through the Data
Integration API.

Tracks Jira epic **CORE-2346**. This is the MVP provider phase that follows the
`riverctl` POC in the parent repo — see `../README.md` and
`../docs/CORE-2346-design.md`.

> The customer-facing term is **data flow**; the underlying API path is
> `/rivers`. The public surface uses Data Integration terminology
> (`boomi_data_flow`).

## Resources

| Resource             | Scope     | API path        | CRUD | Import |
|----------------------|-----------|-----------------|------|--------|
| `boomi_environment` | account   | `/environments` | ✅    | ✅ `<id>` |
| `boomi_connection`  | env       | `/connections`  | ✅    | ✅ `<env_id>/<id>` |
| `boomi_data_flow`   | env       | `/rivers`       | ✅    | ✅ `<env_id>/<id>` |

All resources support `import`, drift detection via `Read`, and force-replace on
immutable fields (`environment_id`, connection `type`).

## Layout

```
main.go                              provider entrypoint (registry addr boomi/data-integration)
internal/client/                     Data Integration API client (Go port of ../rivery_client.py)
internal/provider/                   provider + resource implementations
examples/                            runnable example configuration
docs/                                Terraform Registry documentation
.goreleaser.yml                      signed multi-platform release build
```

## Authentication

Credentials resolve from provider attributes or environment variables (attribute
wins): `token` / `DATA_INTEGRATION_API_TOKEN`, `account_id` /
`DATA_INTEGRATION_ACCOUNT_ID`, `api_url` / `DATA_INTEGRATION_API_URL` (default
`https://api.rivery.io`), `environment_id` / `DATA_INTEGRATION_ENVIRONMENT_ID`
(default environment for env-scoped resources).

## Develop

```bash
make build       # build ./bin/terraform-provider-data-integration
make test        # unit tests (acceptance tests auto-skip without TF_ACC)
make fmt vet     # format + vet
```

To try the provider against the examples without publishing, use a dev override:

```bash
cat > dev.tfrc <<EOF
provider_installation {
  dev_overrides { "boomi/data-integration" = "$(pwd)/bin" }
  direct {}
}
EOF
make build
cd examples && TF_CLI_CONFIG_FILE=../dev.tfrc terraform plan
```

## Acceptance tests

Acceptance tests perform real CRUD + `destroy` per step and assert idempotency
and import-clean state. They run only with `TF_ACC=1` and live integration
credentials:

```bash
export DATA_INTEGRATION_API_TOKEN=... DATA_INTEGRATION_ACCOUNT_ID=... \
       DATA_INTEGRATION_ENVIRONMENT_ID=... RIVERY_ACC_SUBRIVER_ID=<a real river cross_id>
make testacc
```

## Verification status (live integration)

- **`boomi_data_flow` — verified.** `TestAccDataFlowResource` passes against
  `api.integration.rivery.in`: create → import-verify → update → destroy, with
  idempotency. This confirmed the read≠write handling — the API enriches
  `logic_steps`/`settings` on write, so `properties_json`/`settings_json` are
  treated as **config-authoritative** (kept from config, not refreshed from the
  API; drift inside the blob is not detected).
- **`boomi_environment` — auth path verified, create blocked by permissions.**
  The provider correctly authenticated and called the API, which returned
  `403 Insufficient permissions` on environment creation — the integration CLI
  token is environment-scoped, not account-admin. Run with an account-admin
  token to exercise `TestAccEnvironmentResource`.
- **`boomi_connection` — not yet live-tested.** Needs a valid connection
  `type` + `parameters_json` for the target account; confirm `/connections`
  scoping on first run.

## Known gaps / next steps

- **Endpoint paths** for `connection` are modelled from the POC client + design
  note; confirm `/connections` scoping against the live API when running its
  `TF_ACC` test.
- **Generated client** — the design note targets generating the client from the
  public OpenAPI spec. This MVP hand-writes the client and resources; swapping in
  a generated client layer is the next structural step.
- **Auth TTL/refresh** for unattended `apply` — bearer token works; refresh is
  the open question carried from the epic.
- Additional resources (`boomi_dataframe`, `boomi_variable`, …) extend the
  same client + resource pattern.
