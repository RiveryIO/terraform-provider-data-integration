---
page_title: "Connections"
subcategory: "Authentication & connections"
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
    account_name          = "xy12345.us-east-1"
    username              = "SVC_USER"
    default_database_name = "ANALYTICS"
    authentication_type   = "key_pair"
  })

  file_params = {
    key_file_path = "${path.module}/keys/snowflake_rsa_key.p8"
  }
}
```

Note the property names above are the ones the **API** uses, taken from
`GET /v1/connections_types/snowflake` — `account_name`, not `account`;
`default_database_name`, not `database`; and the key-pair upload key is
`key_file_path`, not `private_key_file_path`. Snowflake's own console labels
differ from all three. Always confirm against the `connection_type` data
source above rather than transcribing names from a vendor UI.

- `file_params` — map of property name → **local file path**. The provider
  uploads the file's current contents and substitutes the server-assigned
  path into the request in place of the local path. Sensitive.
- `file_params_content` — map of property name → **raw content** (a string
  already in memory — e.g. from an ephemeral resource — never written to
  local disk). Write-only, never stored in state. Use this instead of
  `file_params` when the key material shouldn't touch the filesystem at all.
- `file_params_content_filenames` — the filename recorded for a
  `file_params_content` entry. **Set this for every `file_params_content`
  entry.** See the warning below.
- The deprecated `ssh_pkey_file`/`ssh_pkey_file_path` attributes predate
  `file_params` and should not be used in new configurations — use
  `file_params = { ssh_pkey_file_path = "<local path>" }` instead.

Whichever map you use, do not also set that same property inside
`parameters_json` — the provider merges the uploaded path into the request
body under that exact key, so setting it in both places conflicts.

!> **Always pair `file_params_content` with `file_params_content_filenames`.**
The API validates uploads by file extension. When you omit the filename, the
upload is named after the *field* — which has no extension — and the API
rejects it with a message that blames the connector instead of the missing
filename:
>
> `API error 400: "File with extension ssh_pkey_file_path is not supported for connection type mysql"`
>
> That reads like "mysql doesn't support this field", which sends you looking
> in the wrong place entirely. The actual fix is one line:

```hcl
file_params_content = {
  ssh_pkey_file_path = local.ssh_private_key # from an ephemeral resource
}
file_params_content_filenames = {
  ssh_pkey_file_path = "ssh_key.pem" # ← the extension is what gets validated
}
```

Use the extension the connector's schema calls for — `.p8` for Snowflake's
`key_file_path`, `.json` for a GCS/BigQuery service-account file, `.pem` for
an SSH private key. `GET /v1/connections_types/{type}` reports the expected
`file_type` per field.

## Updating a connection: write-only attributes don't produce a diff on their own

`parameters_json` and `file_params_content` are
[write-only](https://developer.hashicorp.com/terraform/language/resources/ephemeral#write-only-arguments) —
their values are never stored in state, which is what keeps credentials out
of it. The trade-off: Terraform has no prior value to compare against, so
**changing only a write-only attribute may not register as a change at all.**

If you edit `parameters_json` (say, to add SSH-tunnel fields to an existing
connection) and leave `name`, `type`, and every other ordinary attribute
untouched, `terraform plan` can report:

```
No changes. Your infrastructure matches the configuration.
```

The API is never called and the connection keeps its old parameters. Two ways
to force the update through:

```bash
# Option A — targeted replace (no config edit needed)
terraform apply -replace=boomi_data_integration_connection.mysql_source
```

Option B — change an ordinary attribute in the same commit, so the resource
has a real diff to apply (for example, append a note to `name`). This is
worth knowing before you conclude that a credential change "didn't take".

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
