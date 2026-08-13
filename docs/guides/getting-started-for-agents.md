---
page_title: "Getting started for agents"
description: |-
  How an LLM coding agent should read these docs, verify field names against the
  live API, and prove a data flow actually moved rows.
---

# Getting started for agents

This guide is for an **LLM coding agent** authoring `boomi_data_integration_*`
resources. It is a procedure, not a tutorial — read it before you write any
HCL. Humans reading top-to-bottom want [Getting started](./getting-started.md)
instead.

## TL;DR for agents

1. **Read every guide first**, through the Terraform MCP server — not by
   scraping or fetching pages ad hoc.
2. **Verify every `parameters_json` field name against the live API** before you
   apply. Unknown keys are dropped silently.
3. **Done means rows in the target**, not a green `apply`.
4. **Never fabricate** a run id, a row count, or a log line.

### The MCP calls

Use the HashiCorp [Terraform MCP
server](https://github.com/hashicorp/terraform-mcp-server). Call
`search_providers`:

```
search_providers
  provider_namespace:     RiveryIO
  provider_name:          data-integration
  provider_document_type: guides
```

Then call `get_provider_details` on **every** `providerDocID` it returns — all
of them, not a sample, not the ones whose titles look relevant to your task.

!> **Do not stop at `get_provider_capabilities`.** It reports the *count* of
guides but lists only the first three, and `Connections` — which carries the
connector property contract you almost certainly need — is not among them. An
agent that treats that three-item list as the guide index will author a
connection with invented field names. Also note that `service_slug` is a
required argument on these calls but does **not** filter the result.

Without an MCP server, the same guides are published at
<https://registry.terraform.io/providers/RiveryIO/data-integration/latest/docs>.

## 1 — Read the guides, not just the resource pages

The resource pages document the Terraform schema. The **guides** document the
API contracts that schema carries, and they do not repeat themselves on every
resource page.

This matters more here than in most providers because the two payload
attributes are opaque: `properties_json` and `parameters_json` are passed to the
API **verbatim**, and nothing in the provider validates their shape. A typo in a
nested key is not a plan error — it is a clean apply that produces a broken
object. The rules that would have caught it live only in the guides.

Skipping the guides is consistently the single biggest time sink on agent-driven
runs against this provider.

### Which guide answers which task

| Task | Guide |
|---|---|
| Configure provider auth | [Authentication](./authentication.md) |
| Pick a `run_type` / decide which flow guide applies | [Choosing a data flow type](./data-flow-types.md) |
| Find a connector's exact `parameters_json` fields | [Connections](./connections.md) |
| Upload a keyfile, or reach a DB through an SSH tunnel | [Connections](./connections.md) |
| Database source (`multi_tables`, per-table details) | [Database data flows](./database-data-flows.md) |
| SaaS / API connector source | [API connector data flows](./api-connector-data-flows.md), [API connector required settings](./api-connectors.md) |
| Blueprint or Logic flows | [Blueprint data flows](./blueprint-data-flows.md), [Logic data flows](./logic-data-flows.md) |
| Extract only new rows | [Incremental extraction](./incremental-extraction.md) |
| Log-based replication | [CDC data flows](./cdc-data-flows.md) |
| Choose overwrite / append / merge | [Loading methods](./loading-methods.md) |
| Select, rename, or retype columns | [Schema & column mapping](./metadata-and-schema.md) |
| Turn a flow on; understand drift | [Activation](./activation.md) |
| Trigger a run and poll it | [Running data flows](./running-data-flows.md) |
| Environments, variables, groups | [Environments & variables](./environments-and-variables.md) |
| Start from a business scenario | [Integration patterns by use case](./use-cases.md) |
| Copy a working configuration | [Examples](./examples.md) |

## 2 — Source-of-truth precedence

When sources disagree, follow this order:

**live API > these guides > any local notes** (a repo README, a prior run's
scratch file, your own memory of another provider).

The live API is authoritative for `parameters_json` field names.
`GET /v1/connections_types/{type}` returns the property schema for a connection
type — check it for **every** connector you author, *before* you apply.

!> **The connections API silently drops unknown `parameters_json` keys.** A
misspelled field name does not error. It applies cleanly, reports success, and
leaves you with a connection that has no credential. You will discover it much
later, as an opaque timeout on the flow's first run.

From inside Terraform, the same check is available without leaving your
configuration:

- [`boomi_data_integration_connection_type`](../data-sources/data_integration_connection_type.md)
  — the property schema for one type.
- [`boomi_data_integration_connection_types`](../data-sources/data_integration_connection_types.md)
  — the catalog of all types.

-> **The SSH-tunnel fields are the documented exception.** `is_ssh_tunnel`,
`ssh_remote_host`, `ssh_remote_port`, and `ssh_remote_user` are real and
accepted, but the type catalog does not list them — so for the tunnel path the
[Connections](./connections.md#reaching-a-database-through-an-ssh-tunnel) guide
is authoritative, not the API. This is the only place that inversion applies.

When two sources conflict, **say which one you followed** in your output. Do not
resolve it silently — a conflict is a defect report for whichever source is
stale, and it is only useful if you surface it.

## 3 — Done means proven, not applied

Creating and activating a data flow is not evidence that it works. `terraform
apply` stores a definition; it never moves a row.

To prove a flow works:

1. **Baseline the target first.** Query it before the run, so the rows you count
   afterwards are attributable to this run and not to a previous one.
2. **Trigger a run and poll it to a terminal status.** Endpoints, status values,
   and a reference script are in
   [Running data flows](./running-data-flows.md#the-endpoints).
3. **Query the target directly for per-table row counts**, with the warehouse's
   own client. The provider has no visibility into target contents.

!> **A green check is not a row count.** A run can reach `succeeded` — with a
warning attached — having written **zero rows**. Terminal status tells you the
platform finished; only rows in the target tell you the flow worked.

Report the real run id, the real terminal status, and the real per-table counts.

## 4 — Never fabricate

Do not substitute a plausible-looking run id, row count, table name, or log line
for something you did not observe. A fabricated identifier is worse than no
answer: it is indistinguishable from a real one to the person reading your
output.

If you are blocked — missing credentials, insufficient permissions, an
environment that is down — **stop and report the blocker with options.** Do not
fall back to a mocked or simulated check and present it as verification.

Before declaring a blocker, separate **your** environment from **the
platform's**. They fail independently and in both directions:

- A host that refuses a connection from wherever you are running may still be
  perfectly reachable from the platform's worker fleet.
- A host that answers you directly may be unreachable from the workers — in
  which case the flow needs an SSH tunnel even though your own probe succeeded.

Verify from the side that actually runs the extraction before concluding
anything about reachability. See
[Connections](./connections.md#deciding-whether-you-need-one).

## 5 — Keep a run log and a findings file

Lightweight, and worth the few seconds it costs on a long run:

- **Run log** — one line per phase boundary (reading docs, verifying
  credentials, creating connections, authoring the flow, `apply`, trigger and
  poll, querying the target), with a UTC timestamp. Time retries separately from
  first attempts; four tries is a different signal from one slow try.
- **Findings file** — append the moment something is wrong, **before** you fix
  it. Reconstructing at the end loses the verbatim error, which is the part with
  value. Record what you did, the exact error text, what was actually true, and
  how you established it.

If a finding later proves wrong, correct it in place and say so.

## Related

- [Getting started](./getting-started.md) — the human-oriented walkthrough.
- [Integration patterns by use case](./use-cases.md) — scenario-first entry point.
- [Connections](./connections.md) — the connector property contract.
- [Running data flows](./running-data-flows.md) — trigger, poll, verify.
