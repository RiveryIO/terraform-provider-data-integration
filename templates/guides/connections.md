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

### Confirming the upload worked

Check `GET .../connections/<id>` after creating a keyfile-backed connection.
The field that proves the upload landed is the **property name itself** —
`key_file_path` for Snowflake, `ssh_pkey_file_path` for an SSH key — populated
with a server-assigned path.

~> **Do not use `credentials_exists` for this.** On a working Snowflake
key-pair connection `credentials_exists` stays `false`, because no password was
stored — the credential is the uploaded file. It is a useful check for
password-based connectors and actively misleading for key-pair ones.

### A fresh upload is not instantly visible to the worker fleet

There is a propagation delay between the file upload finishing and the file
being readable by the worker that runs a connection test or metadata read. A
check issued immediately after `apply` can fail with a **connector-level error
that blames the credentials rather than the file**:

```
Snowflake connection problem. Please check the connection details or your credentials in
Snowflake. … ('HY000', '[HY000] [Snowflake][Snowflake] (45) Error loading private key
file: No such file or directory. (45) (SQLDriverConnect)')
```

That message sends you to re-check the account name and username, which are
fine. If `key_file_path` is populated on the connection, the upload itself
succeeded — retry the read before concluding the key is wrong.

## Sourcing credentials from a secrets manager

`parameters_json` and `file_params_content` being write-only keeps credentials
out of state at the *destination*. It does nothing about where the value came
from: read a secret with a `data` source and Terraform copies it into state
verbatim, which puts every credential in that secret — API tokens, database
passwords, private keys, service-account JSON — into the state file in plain
text.

Read it with an **ephemeral** resource instead. Ephemeral values are opened,
used, and closed within a single run and are never written to state or plan
files:

```hcl
ephemeral "aws_secretsmanager_secret_version" "creds" {
  secret_id = var.credentials_secret_id
}

locals {
  creds = jsondecode(ephemeral.aws_secretsmanager_secret_version.creds.secret_string)
}

provider "boomi" {
  api_url    = var.api_url
  account_id = var.account_id
  token      = local.creds.api_token
}

resource "boomi_data_integration_connection" "snowflake" {
  name = "Snowflake"
  type = "snowflake"

  parameters_json = jsonencode({
    account_name          = "xy12345.us-east-1"
    username              = "SVC_USER"
    warehouse             = "COMPUTE_WH"
    default_database_name = "ANALYTICS"
    default_schema_name   = "PUBLIC"
    authentication_type   = "key_pair"
  })

  # The PEM goes straight from the secret to the upload — never on local disk.
  file_params_content           = { key_file_path = local.creds.snowflake.private_key }
  file_params_content_filenames = { key_file_path = "snowflake_key.p8" }
}
```

The rules that make this work, and the ones that bite:

- **An ephemeral-derived value may only flow into provider configuration, a
  write-only resource argument, or another ephemeral resource.** Terraform
  rejects anything else — a plain resource argument, a `data` block, an
  `output`. That restriction is not an inconvenience; it is the mechanism that
  guarantees the value cannot reach state.
- **Use `file_params_content`, not `file_params`, for a secret-sourced file.**
  `file_params` takes a *path*, so the material has to exist on disk first.
  `file_params_content` takes the content directly from memory. Pair it with
  `file_params_content_filenames` — see the warning above.
- **Never use `local_sensitive_file`** to materialize a key before uploading it.
  It writes the file's content into state, defeating the whole arrangement.
- **One secret need not carry everything.** If the credential for one connector
  lives in a different secret from the rest, add a second `ephemeral` block and
  read just that key from it. That is cheaper and less disruptive than merging
  secrets to suit Terraform.
- **Requires Terraform >= 1.11** (write-only arguments) and, for this example,
  `hashicorp/aws` >= 6.0 (the ephemeral Secrets Manager resource). The same
  shape works with any provider that offers an ephemeral secret resource.

Rotating a credential is then just an `apply` — the new value is read from the
secret and written to the connection, with nothing to clean up locally.

## Reaching a database through an SSH tunnel

Databases that aren't publicly routable — or that only allow a bastion host —
are reached through an SSH tunnel. This is configured entirely inside
`parameters_json`, plus the private key as a file upload.

!> **The tunnel fields are not listed by
`GET /v1/connections_types/{type}`, and therefore not by the
`boomi_data_integration_connection_type` data source either.** Everywhere else
in this guide that catalog is authoritative for what `parameters_json` accepts.
For the SSH-tunnel path it is not — the fields below are real and accepted, but
you cannot discover them from it. This section is the reference.

```hcl
resource "boomi_data_integration_connection" "mysql_tunneled" {
  environment_id = var.environment_id
  name           = "MySQL via bastion"
  type           = "mysql"

  parameters_json = jsonencode({
    # The database, addressed as the BASTION sees it — usually a private
    # address or internal DNS name, not one you can reach yourself.
    host     = "db.internal"
    port     = 3306
    database = "app"
    username = "readonly"
    password = var.mysql_password

    # The tunnel itself.
    is_ssh_tunnel   = true
    ssh_remote_host = "bastion.example.com" # the jump host
    ssh_remote_port = 22
    ssh_remote_user = "tunnel_user"
  })

  # The SSH private key. file_params_content keeps it off local disk and out
  # of state; the .pem filename is required (see the warning above).
  file_params_content = {
    ssh_pkey_file_path = var.ssh_private_key
  }
  file_params_content_filenames = {
    ssh_pkey_file_path = "ssh_key.pem"
  }
}
```

| Field | Meaning |
| --- | --- |
| `is_ssh_tunnel` | `true` turns the tunnel on. Absent or `false` means a direct connection. |
| `ssh_remote_host` | The bastion/jump host to connect through — **not** the database. |
| `ssh_remote_port` | The bastion's SSH port, usually `22`. |
| `ssh_remote_user` | The SSH user on the bastion. |
| `ssh_pkey_file_path` | Key for the bastion, via `file_params` or `file_params_content`. Not the database password. |

`host`/`port` stay the **database's** address as resolved from the bastion.
A common mistake is pointing `host` at the bastion; the bastion goes in
`ssh_remote_host` and nowhere else.

### Deciding whether you need one

The trap: **a database being reachable from your machine tells you nothing
about whether the platform can reach it.** Runs execute on the platform's
worker fleet, which egresses from entirely different addresses than your
laptop or CI runner. A source whose firewall allows your office range but only
the bastion otherwise will connect perfectly from `mysql` on your terminal and
fail from every run.

Symptoms of a missing tunnel, in the order you'll meet them:

- `boomi_data_integration_connection_test` returns `success = false` with a
  connect-timeout error — **this is the cheap one**, which is why the test is
  worth attaching to every connection.
- Without that test: the flow applies and activates cleanly, then every run
  sits in `running` and dies on the platform's watchdog, reporting a connect
  timeout (`[Errno 110]` for MySQL/Postgres) with nothing indicating the
  connection was the cause.

If you have a working connection to the same host made outside Terraform —
through the console, or by another team — inspect it and copy its tunnel
settings rather than deriving them. Whoever set it up already discovered
which bastion the host expects.

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
