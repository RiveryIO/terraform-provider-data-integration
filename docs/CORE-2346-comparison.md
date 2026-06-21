# CORE-2346 — BDI plugin `RiveryAPI` vs. the Terraform-Provider plan

Comparison note. Scope: **only** what the `boomi-bdi-copilot` plugin's API client
(`tools/rivery_api.py`, v1.11.0) tells us about the CORE-2346 Terraform-Provider plan —
what it confirms, what it changes, and what it hands us for free.

## TL;DR

The BDI plugin is a **production, customer-facing reference client for the exact public
API the provider must wrap.** It de-risks the plan's #1 open question (auth) and already
solved the drift/normalization problems the plan flagged as the biggest engineering risk.
Net effect: the provider's client layer is largely a **port of patterns that already exist
and work**, not greenfield discovery.

## Point-by-point

| CORE-2346 plan item | What the BDI plugin shows | Verdict |
|---|---|---|
| **#1 open question: customer auth model** (internal research only saw a 15-min Boomi-JWT→token exchange) | Customers authenticate with a **plain `Bearer` JWT** held as `DATA_INTEGRATION_API_TOKEN`. Region auto-detected by probing `/environments` (US `api.rivery.io` → EU `eu-api.rivery.io`). JWT `acc` claim decoded locally; **no `/users/me`**. | **Resolved-ish.** A usable bearer-token model exists today. Still confirm the token's **TTL / issuance / refresh** story for unattended `terraform apply` (CI). This is the one remaining auth question, much narrower than before. |
| **Provider host/base-url config** | `base_url` is overridable (`DATA_INTEGRATION_API_URL`), defaults to `api.rivery.io`; per-region hosts are real. Internal envs follow `api.<env>.rivery.in` (e.g. `api.integration.rivery.in`). | Provider config block = `host` (region/base-url) + `token` + optional default `account_id`/`environment_id`. Mirrors BDI's constructor 1:1. |
| **Biggest eng risk: river-definition drift / noisy plans** | `edit_river` = **read-modify-write**: GET current → `_deep_merge(patch)` → PUT full body. Lists (`schedulers`, `logic_steps`) are **full-replace**, not index-merged. | The provider's `Update` should use the *same* read-merge-write; the merge/replace rules are already worked out. Lifts straight over. |
| **API rejects unknown fields on write** | `_RIVER_WRITE_FORBIDDEN_FIELDS` strips `title,id,cross_id,_id,account_id,environment_name,group_name` before PUT/POST (`extra_forbidden`). | Provider must strip server-only/computed attrs before write. Exact list provided. Prevents a whole class of 422s. |
| **Read normalization** | API is inconsistent: list uses `river_cross_id`/`name`, detail uses `cross_id`/`name`. BDI normalizes → `id`/`title` (idempotent). | Provider `Read` needs the same normalization to keep state stable. Pattern provided. |
| **ID / import strategy** (plan: `account/env/cross_id`) | `cross_id` is the stable river identity; list-vs-detail aliasing handled. | Confirms `cross_id` as the resource ID; composite import key stands. |
| **MVP resources mapped to endpoints** | Confirmed live (`reference/api-coverage.md`): rivers CRUD, runs, activities, connections (create/list/types), variables (`PUT`), environments (list). | The MVP set (environment, connection, variable, river) all have real v1 endpoints. |
| **"API gaps" theme** | **Promote / pull-requests** (`PUT console.rivery.io/api/pull/{level}` + poll) and **logicode upload** (`console.rivery.io/api/files/...`) live **only on the legacy console API**, not v1 — and need browser-style `Origin`/`Referer` headers. | Concrete gap confirmed: a **v1-only provider cannot do promote/CI-pull or logicode** without either calling the console API or new v1 endpoints. Scope these out of MVP or file API work. |
| **Resilience / error mapping → TF diagnostics** | Typed exceptions per status (`AuthError` 401/403, `NotFound` 404, `Validation` 422, `RateLimit` 429, `Transient` 5xx) + domain (`WindowTooLargeError`, `LogsNotRetainedError`); 3× retry/backoff on 5xx+conn. | Drop-in mapping for the provider's diagnostics + retry layer. Domain errors (runs-window cap, log retention) are good-to-know but MVP-irrelevant. |
| **Connection write-only secrets (drift)** | (Plan-only; BDI doesn't round-trip connection secrets either.) | Unchanged — still a real provider concern; use TF `WriteOnly` + keep secret in state, never overwrite from `Read`. |
| **Telemetry / attribution** | Every call sends `User-Agent` + `X-Boomi-Plugin: …(account=…)` from `plugin.json`. | Provider should send an equivalent `X-Boomi-Plugin: terraform-provider-rivery/<ver>` header — feeds the existing NR Boomi-plugin-usage dashboard for free. |

## What changes in the plan

1. **Auth (Phase 0 blocker) is largely answered** — bearer-token model exists; reduce the
   open question to "token TTL + refresh for unattended apply."
2. **Phase 1 client work shrinks** — port BDI's `RiveryAPI` patterns (deep-merge edit,
   forbidden-field strip, field normalization, typed errors, region probe) rather than
   re-derive them. Whether the provider is Go (generated from the public OpenAPI) or a thin
   Python POC, the *semantics* are settled.
3. **A concrete API-gap to log against rivery-api-service**: promote/pull-request + logicode
   are console-API-only. Either exclude from the provider's declarative surface or add v1
   endpoints.

## What does NOT change

- River-definition modeling is still the hard part — BDI passes the river config through
  largely opaquely (templates + deep-merge), which is exactly the "typed core attrs +
  JSON passthrough" the plan proposed. No free lunch on full declarative validation.
- Registry publishing, Go scaffolding, acceptance-test harness — unaffected; BDI is a
  Python client, not a TF provider, so it informs the **client layer only**.
