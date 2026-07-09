# source_to_target → BigQuery example

Authors a `source_to_target` river targeting **BigQuery** with the
`boomi/data-integration` provider. Modeled on a real integration river (ECB
exchange rates → BigQuery): `properties.json` is the exact `source_to_target`
body the API accepts (source + 10-column mapping + `loading_method=overwrite`);
`main.tf` creates a fresh `gcloud` connection and wires its id + the target
`dataset_id` into that body.

## ⚠️ Known limitation — authoring works, running does not (yet)

A functional BigQuery `gcloud` connection needs an **uploaded service-account key
file**. The provider creates connections from JSON only and **cannot upload a key
file**, so a river using a provider-created BigQuery connection:

- **creates** fine (river + connection resources apply cleanly), but
- **fails activation** with `Dataset <name> was not found in BigQuery`
  (`[RVR-ACTIVATE-500]`) — the keyless connection can't authenticate to the GCP
  project, so the platform's dataset validation fails — and therefore
- **cannot run** (`POST /run` → `400 "Data Flow ... is disabled"`).

This was verified end-to-end on integration (two datasets tried, both failed
activation on the keyless connection). To actually run it, finish the connection
out-of-band — upload the service-account key in the console — then re-activate.
It's a provider gap for **file-backed connection types** (gcloud / SSH keys),
tracked under CORE-2346.

## Secrets

No credentials are committed. Provider auth comes from the
`DATA_INTEGRATION_API_TOKEN` env var; GCP identifiers come from
`terraform.tfvars` (git-ignored). `properties.json` is river structure only — no
secrets. Never commit `terraform.tfvars` or `*.tfstate`.

## Run it

```bash
export DATA_INTEGRATION_API_URL=https://api.integration.rivery.in
export DATA_INTEGRATION_ACCOUNT_ID=<account_id>
export DATA_INTEGRATION_ENVIRONMENT_ID=<existing_env_id>
export DATA_INTEGRATION_API_TOKEN=<token>

cp terraform.tfvars.example terraform.tfvars   # then edit
terraform apply   # creates the connection + river (river starts disabled)
# Upload the SA key on the connection in the console, then activate + run.
```
