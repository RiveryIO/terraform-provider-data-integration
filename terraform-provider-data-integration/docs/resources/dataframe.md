---
page_title: "boomi_dataframe Resource"
description: |-
  A Data Integration dataframe, keyed by name within an environment.
---

# boomi_dataframe (Resource)

A Data Integration dataframe. Dataframes are environment-scoped and keyed by
their unique **name** — the API has no separate id, so the resource uses the
name as its Terraform id.

## Example Usage

```terraform
resource "boomi_dataframe" "my_df" {
  environment_id = boomi_environment.prod.id
  name           = "daily-export"

  connection_settings = {
    connection     = boomi_connection.s3.id
    datasource_id  = "aws"
    storage_type   = "s3"
    default_bucket = "rivery-dev-tests"
  }
}
```

## Schema

### Required

- `name` (String) Dataframe name. Must be unique within the environment. The API
  does not support renaming, so changing it forces a new dataframe.

### Optional

- `environment_id` (String) Environment this dataframe belongs to. Falls back to
  the provider-level `environment_id`. Changing it forces a new dataframe.
- `connection_settings` (Attributes) Storage connection settings for the
  dataframe's parquet files. The only field the API allows to be updated in
  place.

### Nested Schema for `connection_settings`

Required:

- `connection` (String) ID of the storage connection — typically a reference to
  a `boomi_connection`.
- `datasource_id` (String) Datasource identifier of the connection (e.g. `s3`,
  `gcs`).
- `storage_type` (String) Storage type (e.g. `s3`, `aws`, `gcs`).
- `default_bucket` (String) Default bucket the dataframe writes its parquet
  files to.

### Read-Only

- `id` (String) Resource id. Equals the dataframe `name`.

## Import

```shell
# environment-qualified
terraform import boomi_dataframe.my_df <environment_id>/<name>

# bare name (requires provider-level environment_id)
terraform import boomi_dataframe.my_df <name>
```

`connection_settings` is config-authoritative — after import, declare it in
configuration; it is reconciled from config rather than refreshed from the API.
