# terraform-provider-data-integration

Terraform provider for **Boomi Data Integration** (Rivery). Declare environments,
connections, and data flows in `.tf`, plan the diff, and apply through the Data
Integration API.

Tracks Jira epic **CORE-2346**. This is the MVP provider phase that follows the
`riverctl` POC in the parent repo — see `../README.md` and
`../docs/CORE-2346-design.md`.

> The customer-facing term is **data flow**; the underlying API path is
> `/rivers`. The public surface uses Data Integration terminology
> (`boomi_data_integration_data_flow`).

## Resources

| Resource             | Scope     | API path        | CRUD | Import |
|----------------------|-----------|-----------------|------|--------|
| `boomi_data_integration_environment` | account   | `/environments` | ✅    | ✅ `<id>` |
| `boomi_data_integration_connection`  | env       | `/connections`  | ✅    | ✅ `<env_id>/<id>` |
| `boomi_data_integration_data_flow`   | env       | `/rivers`       | ✅    | ✅ `<env_id>/<id>` |

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

- **`boomi_data_integration_data_flow` — verified.** `TestAccDataFlowResource` passes against
  `api.integration.rivery.in`: create → import-verify → update → destroy, with
  idempotency. This confirmed the read≠write handling — the API enriches
  `logic_steps`/`settings` on write, so `properties_json`/`settings_json` are
  treated as **config-authoritative** (kept from config, not refreshed from the
  API; drift inside the blob is not detected).
- **`boomi_data_integration_environment` — verified (create → read → destroy).** Confirmed against
  `api.integration.rivery.in` with an account-admin token. This exposed and fixed
  a read-mapping bug: the API returns the id/name under `cross_id`/`_id` and
  `environment_name`, which the resource now normalizes (see `normalizeID`).
  Delete is a **soft-delete** server-side (`is_deleted: true`); the record remains
  readable. With an environment-scoped (non-admin) token, create returns a clear
  `403 insufficient permissions` diagnostic.
- **`boomi_data_integration_connection` — verified (create → read → destroy, idempotent).**
  Confirmed live with a `redshift` connection. Two fixes were required: the
  create/update body must use `connection_name`/`connection_type` (not the generic
  `name`/`type`), and the read response maps `connection_name`/`connection_type_id`
  via `normalizeID`. Connection writes are environment-scoped — the token must hold
  the right role on the target `environment_id`.

- **`boomi_data_integration_dataframe` — verified (create → read → destroy, idempotent).**
  Confirmed live referencing an existing S3 connection. Dataframes are
  environment-scoped and keyed by **name** (no cross_id), so the resource uses
  the name as its id; `connection_settings` is a typed nested block that
  cross-references a `boomi_data_integration_connection`. Delete is a hard delete (GET → 404),
  unlike the environment soft-delete.

- **`boomi_data_integration_variable` — verified (create → read → in-place update → destroy, idempotent).**
  Confirmed live against env `5ffeb0…`. Variables are an environment-scoped
  key/value collection; each key is its own resource and writes merge (sibling
  keys untouched). Requires a token with explicit `variables:list`/`variables:edit`
  scopes — a `role:admin`-only env grant returns `403`.

- **`boomi_data_integration_data_flow_cdc_config` — CRUD verified; not exercised on a true CDC river.**
  Manages a CDC river's source offset (mysql binlog / pg+mssql lsn / mongo resume
  token / oracle scn) via `config_json`. Create/update (single `POST`) and delete
  (`DELETE`) verified live against a real river; the offset GET validates CDC-only,
  so a genuinely CDC-enabled river is needed to exercise reads. Config-authoritative
  (the offset advances at runtime; not drift-reconciled) — intended to seed/reset.

### Auth error reporting

`401` and `403` responses surface as actionable Terraform diagnostics (distinct
"authentication failed" vs "insufficient permissions" messages with remediation
hints). Programmatic callers can branch via `errors.Is(err, client.ErrUnauthorized)`,
`client.ErrForbidden`, or the umbrella `client.ErrAuth`.

## Known gaps / next steps

- **Generated client** — the design note targets generating the client from the
  public OpenAPI spec. This MVP hand-writes the client and resources; swapping in
  a generated client layer is the next structural step.
- **Auth TTL/refresh** for unattended `apply` — bearer token works; refresh is
  the open question carried from the epic.
- Additional resources (`boomi_data_integration_dataframe`, `boomi_data_integration_variable`, …) extend the
  same client + resource pattern.
