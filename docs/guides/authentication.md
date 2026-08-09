---
page_title: "Authentication - boomi Provider"
description: |-
  Set up the provider with either a Data Integration API token or Boomi Platform credentials.
---

# Authentication

The provider supports two ways to authenticate. Exactly one must resolve — the
provider infers which one you mean from which attributes (or environment
variables) are actually populated, so there is no separate mode switch to set.

| | Native API token | Boomi Platform JWT |
|---|---|---|
| You provide | one long-lived `token` | four `boomi_*` values |
| What rides on the request | the token itself, unchanged | a JWT minted from your Boomi credentials |
| Best for | teams already issuing Data Integration tokens today | teams that already hold Boomi Platform credentials and don't want to hand-mint a separate token |

---

## Option 1 — Native API token

The provider's original, still fully supported mode.

```hcl
provider "boomi" {
  api_url    = "https://api.rivery.io"
  account_id = var.account_id
  token      = var.api_token
}
```

| Attribute | Environment variable | Required | Notes |
|---|---|---|---|
| `token` | `DATA_INTEGRATION_API_TOKEN` | Yes (in this mode) | Sensitive. Settings → API Tokens → Generate. |
| `account_id` | `DATA_INTEGRATION_ACCOUNT_ID` | Always | Needed in every auth mode — see below. |
| `environment_id` | `DATA_INTEGRATION_ENVIRONMENT_ID` | No | Default environment for resources that don't set their own. |
| `api_url` | `DATA_INTEGRATION_API_URL` | No | Defaults to `https://api.rivery.io`. |

---

## Option 2 — Boomi Platform JWT

Use this if your team already manages Boomi Platform credentials and would
rather not maintain a separate Data Integration token. The provider exchanges
your long-lived Boomi Platform `api_token` for a short-lived JWT automatically
— on first use, cached for the rest of the run, and re-exchanged transparently
if a request ever comes back `401` mid-`apply`. You never see the JWT itself.

```hcl
provider "boomi" {
  api_url            = "https://api.rivery.io"
  account_id         = var.account_id                # still required — see note below
  boomi_platform_url = "https://api.boomi.com"
  boomi_account_id   = var.boomi_account_id
  boomi_username     = var.boomi_username
  boomi_api_token    = var.boomi_api_token
}
```

| Attribute | Environment variable | Required | Notes |
|---|---|---|---|
| `boomi_platform_url` | `DATA_INTEGRATION_BOOMI_PLATFORM_URL` | No | Defaults to `https://api.boomi.com`. |
| `boomi_account_id` | `DATA_INTEGRATION_BOOMI_ACCOUNT_ID` | Yes (in this mode) | The Boomi Platform account, **not** the Data Integration `account_id` below — see note. |
| `boomi_username` | `DATA_INTEGRATION_BOOMI_USERNAME` | Yes (in this mode) | The Boomi Platform user the token belongs to. |
| `boomi_api_token` | `DATA_INTEGRATION_BOOMI_API_TOKEN` | Yes (in this mode) | Sensitive. Boomi Platform → account settings → API Tokens. Tokens are bound to the user who created them and cannot be transferred. |
| `account_id` | `DATA_INTEGRATION_ACCOUNT_ID` | Always | See note. |

-> **`account_id` is still required, in both modes.** The Boomi JWT resolves
*who* is asking (via the Boomi Platform account tied to `boomi_account_id`) —
it does not pick *where* the request goes. Every API path is
`/v1/accounts/{account_id}/...`, and that `account_id` is the Data Integration
account, set the same way as in the native-token mode. Getting a valid JWT for
the right Boomi account still results in a `403` if that identity's role
doesn't cover the specific `account_id`/`environment_id` you're targeting —
that's a permissions problem, not an authentication one.

---

## Choosing between them

There's no `auth_mode` attribute to set — the provider looks at which fields
are actually populated and validates that exactly one mode resolves:

| What's set | Result |
|---|---|
| `token` only | Native mode |
| all four `boomi_*` values only | Boomi JWT mode |
| `token` **and** any `boomi_*` value | Error: *"Conflicting authentication configuration... set either token or the Boomi Platform credentials — not both."* |
| neither | Error: *"Missing authentication... set token / DATA_INTEGRATION_API_TOKEN, or supply Boomi Platform credentials."* |
| some but not all `boomi_*` values | Error naming the specific missing attribute (e.g. *"Missing Boomi API token"*) |

Both modes work identically for every resource and data source in the
provider — nothing else in your configuration changes based on which one you
pick.
