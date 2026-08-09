# source_to_target → BigQuery example

Authors a `source_to_target` data flow targeting **BigQuery** with the
`riveryio/data-integration` provider. Modeled on a real integration data flow (ECB
exchange rates → BigQuery): `properties.json` is the exact `source_to_target`
body the API accepts (source + 10-column mapping + `loading_method=overwrite`);
`main.tf` creates a fresh `gcloud` connection and wires its id + the target
`dataset_id` into that body.

## ⚠️ Known limitation — authoring works, running does not (yet)

A functional BigQuery `gcloud` connection needs an **uploaded service-account key
file**. The provider creates connections from JSON only and **cannot upload a key
file**, so a data flow using a provider-created BigQuery connection:

- **creates** fine (data flow + connection resources apply cleanly), but
- **fails activation** with `Dataset <name> was not found in BigQuery`
  (`[RVR-ACTIVATE-500]`) — the keyless connection can't authenticate to the GCP
  project, so the platform's dataset validation fails — and therefore
- **cannot run** (`POST /run` → `400 "Data Flow ... is disabled"`).

This was verified end-to-end against a live account (two datasets tried, both
failed activation on the keyless connection). To actually run it, finish the
connection out-of-band — upload the service-account key in the console — then
re-activate. It's a provider gap for **file-backed connection types**
(gcloud / SSH keys).

## Secrets

No credentials are committed. Provider auth comes from the
`DATA_INTEGRATION_API_TOKEN` env var; GCP identifiers come from
`terraform.tfvars` (git-ignored). `properties.json` is data flow structure only — no
secrets. Never commit `terraform.tfvars` or `*.tfstate`.

## Run it

Set `DATA_INTEGRATION_API_TOKEN` / `DATA_INTEGRATION_ACCOUNT_ID` (see the
[Authentication](../../docs/guides/authentication.md) guide), then:

```bash
cp terraform.tfvars.example terraform.tfvars   # then edit
terraform apply   # creates the connection + data flow (data flow starts disabled)
# Upload the SA key on the connection in the console, then activate + run.
```
