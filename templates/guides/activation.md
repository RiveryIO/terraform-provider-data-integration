---
page_title: "Activation - boomi Provider"
subcategory: "Activation"
description: |-
  How activate manages a data flow's running state, and how drift is reconciled.
---

# Activation

`activate` on `boomi_data_integration_data_flow` is the desired activation
state — but it has three states, not two, and the sequence behind `true` is
not a single API call.

## The three states

| `activate` | Behaviour |
| --- | --- |
| `true` | The provider enables the flow: `disable` (only if currently active) → `update` → `enable_cdc` (CDC flows only) → `activate`. |
| `false` | The provider disables the flow if it is currently active. |
| omitted | Activation is **not managed**. There is deliberately no default: the provider adopts whatever the server reports (a newly created data flow is disabled) and never activates or disables the flow on later applies. |

## Why activating isn't just one call

The `update` step in that sequence is not redundant. A data flow created
through the API is not yet activatable — it has to be updated once before the
activate call will accept it, and skipping that update fails activation with
`RVR-ACTIVATE-500` on every API-created flow. The provider always issues that
update itself, so you never have to sequence it yourself; this only matters
if you're calling the API directly outside Terraform.

CDC flows have one extra step: `enable_cdc` sets `ENABLE_LOG` on the flow,
and has to happen after `update` but before the final `activate` call. See
[CDC Data Flows](./cdc-data-flows.md) for the full CDC-specific sequence and
scheduler requirements that go alongside it.

## Drift and the read-only `status`

Refresh reconciles `activate` against the API's `metadata.river_status`, so
an activate/disable performed outside this resource — the console, the API
directly, or the deprecated `data_integration_data_flow_run` resource — shows
up as drift on the next `plan` and, when `activate` is set explicitly, is
corrected on the next `apply`. When the API omits `river_status` (observed on
a small fraction of data flows) the previously known value is kept.

The read-only `status` attribute on the resource exposes the raw server
value, if you need to branch on it elsewhere in your configuration.

## Running a flow, vs. activating it

Activation and *running* a flow are different concepts.
`boomi_data_integration_data_flow_run` used to be how you'd trigger a
one-off run through Terraform — it's deprecated now, in favor of setting
`activate` and letting the platform's own scheduler run the flow. Terraform
manages desired state; it isn't the right tool for triggering an imperative
action on every `apply`.
