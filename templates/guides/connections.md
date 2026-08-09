---
page_title: "Connections - boomi Provider"
subcategory: "Connections"
description: |-
  Connection types, discovering the right properties, keyfile uploads, and file-zone linking.
---

# Connections

A `boomi_data_integration_connection` authenticates to one system — a
database, a SaaS API, or a file-zone staging location. This guide covers
everything about setting one up correctly: finding out which properties a
given connection or target type actually needs, uploading file-backed
credentials, and linking a connection to its file-zone staging connection.

## Finding the right properties for a connection type

`parameters_json` is connection-type-specific — a Snowflake connection and a
Jira connection need entirely different fields, and guessing them from
examples doesn't scale. Two data sources read the live property schema
straight from the API, so you never have to guess:

**All connection types at once**, `boomi_data_integration_connection_types`:

```hcl
data "boomi_data_integration_connection_types" "all" {}
```

Use this to browse what's available, or to confirm a `type` string before
you commit to it.

**One connection type's exact property list**, `boomi_data_integration_connection_type`:

```hcl
data "boomi_data_integration_connection_type" "snowflake" {
  connection_type = "snowflake"
}

output "snowflake_properties" {
  value = data.boomi_data_integration_connection_type.snowflake.property_names
}
```

`property_names` is the list of keys valid inside that connection's
`parameters_json` — exactly the keys the live API will accept, no more, no
less. `properties_json` (singular object, not to be confused with the
connection resource's `parameters_json`) carries the full property schema —
types, whether a field is required, whether it's a file upload — as raw JSON,
for building your own tooling on top of it.

## Finding the right properties for a target

Targets aren't connections — a target's shape depends on the **combination**
of connection type and data-flow type. `boomi_data_integration_target_types`
is the equivalent catalog for the target side:

```hcl
data "boomi_data_integration_target_types" "all" {}
```

Each entry gives you `target_type`, `connection_type` (which connection this
target binds to), `logic_step_type`, and `data_flow_type_id` — filter the list
client-side (e.g. in a `for` expression) down to the connection type you're
targeting to see which target shapes are actually available for it, before
you write the `target` block in a data flow's `properties_json`.

## Keyfile-backed credentials

Some connection types authenticate with a file, not a string — a Snowflake
key-pair `.p8` private key, a GCS/BigQuery service-account JSON file, an SSH
private key for an SFTP connection. Two write-only map attributes handle
this, keyed by whichever property name the connection type's schema calls
for (found via `connection_type` above):

```hcl
resource "boomi_data_integration_connection" "snowflake_keypair" {
  name = "Snowflake (key-pair auth)"
  type = "snowflake"

  parameters_json = jsonencode({
    account  = "xy12345.us-east-1"
    username = "SVC_USER"
    database = "ANALYTICS"
  })

  file_params = {
    private_key_file_path = "${path.module}/keys/snowflake_rsa_key.p8"
  }
}
```

- `file_params` — map of property name → **local file path**. The provider
  uploads the file's current contents and substitutes the server-assigned
  path into the request in place of the local path. Sensitive.
- `file_params_content` — map of property name → **raw content** (a string
  already in memory — e.g. from an ephemeral resource — never written to
  local disk). Write-only, never stored in state. Use this instead of
  `file_params` when the key material shouldn't touch the filesystem at all.
- `file_params_content_filenames` — overrides the filename recorded for a
  `file_params_content` entry (some connection types validate the extension).
- The deprecated `ssh_pkey_file`/`ssh_pkey_file_path` attributes predate
  `file_params` and should not be used in new configurations — use
  `file_params = { ssh_pkey_file_path = "<local path>" }` instead.

Whichever map you use, do not also set that same property inside
`parameters_json` — the provider merges the uploaded path into the request
body under that exact key, so setting it in both places conflicts.

## Linking a connection to a file zone

`fz_connection_id` links a connection to the file-zone staging connection
data actually lands in before being loaded to the target. It's optional —
most connection types default sensibly — but connectors that stage through a
file zone (e.g. `aws_fz`) need it set explicitly once you have more than one
file-zone connection and the default choice isn't the one you want:

```hcl
resource "boomi_data_integration_connection" "s3_fz" {
  name = "S3 File Zone"
  type = "aws_fz"

  parameters_json = jsonencode({
    aws_access_key    = var.aws_access_key
    aws_access_secret = var.aws_access_secret
    region            = "us-east-1"
    bucket_name       = "my-data-bucket"
  })
}

resource "boomi_data_integration_connection" "mysql_source" {
  name             = "MySQL"
  type             = "mysql"
  fz_connection_id = boomi_data_integration_connection.s3_fz.id

  parameters_json = jsonencode({
    host     = "db.internal"
    username = "readonly"
    database = "app"
  })
}
```

-> **Known gap:** `examples/connection-s3` in this repo creates an `aws_fz`
connection but doesn't demonstrate linking another connection to it via
`fz_connection_id` — worth fixing in a follow-up examples PR, not addressed
here.
