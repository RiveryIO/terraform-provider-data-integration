# MySQL → Snowflake with INCREMENTAL extraction: backfill from a fixed start
# date, then track forward on an increment column.
#
# This is the provider's first incremental-extraction example. Everything else
# under examples/ is a full reload (extract_method "all"), so if you are copying
# a mapping from another example you are copying a full reload.
#
# Two routes are shown, gated by variables so you can apply either one:
#
#   1. DISCOVERY-DRIVEN (var.create_discovery_driven_flow, default true)
#      boomi_data_integration_source_metadata introspects the live MySQL
#      connection and stamps extract_method/incremental_field/date_range onto
#      every discovered table. Its schemas_json drops straight into
#      properties_json. One increment column for all tables.
#
#   2. HAND-WRITTEN (var.create_hand_written_flow, default false)
#      The same mapping spelled out literally, for per-table control: a
#      different increment column per table, per-table chunk sizes, and
#      modified_columns deltas (deselect/rename).
#
# See README.md for the "incremental" vs "increment" trap, the loading_method
# reasoning, and what is and is not verified.

terraform {
  # v1.1.0 is the FIRST release whose source_metadata data source accepts
  # extract_method / incremental_field / date_range. Earlier versions hardcoded
  # extract_method "all", so route 1 could only ever produce a full reload.
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = ">= 1.1.0"
    }
  }
}

provider "boomi" {
  api_url        = var.api_url
  token          = var.api_token
  account_id     = var.account_id
  environment_id = var.environment_id
}

# ── Source connection: MySQL ──────────────────────────────────────────────────
# Credentials come from variables only (see variables.tf — password is marked
# sensitive). parameters_json is write-only: never stored in state, and the API
# omits secrets on read, so credential drift is not detectable.
resource "boomi_data_integration_connection" "mysql_source" {
  environment_id = var.environment_id
  name           = var.mysql_connection_name
  type           = "mysql"

  parameters_json = jsonencode({
    host            = var.mysql_host
    port            = var.mysql_port
    username        = var.mysql_username
    password        = var.mysql_password
    database        = var.mysql_database
    ssl_mode        = var.mysql_ssl_mode
    connection_desc = "MySQL incremental source, managed by terraform"
  })
}

# ── Target connection: Snowflake ──────────────────────────────────────────────
# Password auth is used here to keep the example short. For key-pair auth, drop
# `password` and upload the .p8 via the connection resource's file_params /
# file_params_content (the latter keeps the key out of local disk AND state).
resource "boomi_data_integration_connection" "snowflake_target" {
  environment_id = var.environment_id
  name           = var.snowflake_connection_name
  type           = "snowflake"

  parameters_json = jsonencode({
    account_name    = var.snowflake_account_name
    username        = var.snowflake_username
    password        = var.snowflake_password
    warehouse       = var.snowflake_warehouse
    role            = var.snowflake_role
    database        = var.snowflake_database
    connection_desc = "Snowflake incremental target, managed by terraform"
  })
}

# ── Shared target block ───────────────────────────────────────────────────────
locals {
  # loading_method "merge" — the deliberate choice for an incremental flow whose
  # increment column is an UPDATE timestamp: a row that changes is re-extracted
  # and must replace its previous version, not sit beside it. "append" would
  # accumulate duplicates. merge_method is only meaningful when loading_method
  # is "merge"; the API defaults it to "merge" but we set it explicitly so the
  # config, not a server-side default, decides. See README for when "append" is
  # the right call instead.
  target = {
    name           = "snowflake"
    connection_id  = boomi_data_integration_connection.snowflake_target.id
    loading_method = "merge"
    merge_method   = var.snowflake_merge_method
    database_name  = var.snowflake_database
    schema_name    = var.snowflake_schema
  }

  # The schedule that drives "track forward". Sent top-level as "schedulers";
  # exactly one scheduler is allowed and the cron must fire between once per day
  # and 12 times per hour.
  #
  # NOTE: a typed `schedule` block (cron_expression + is_enabled) has landed on
  # this repo's main branch and deprecates schedulers_json, but it is NOT in the
  # released v1.1.0 this example pins against. Switch to the typed block once
  # the next version ships.
  schedulers = [{
    cron_expression = var.schedule_cron
    is_enabled      = var.schedule_enabled
  }]

  # Same story for `settings_json`: a typed `settings` block exists on main but
  # not in v1.1.0. A backfill window can run long, so give the flow a real
  # timeout instead of leaving it on automatic calculation.
  settings = {
    run_timeout_seconds = var.run_timeout_seconds
  }
}

# ══════════════════════════════════════════════════════════════════════════════
# ROUTE 1 — DISCOVERY-DRIVEN
# ══════════════════════════════════════════════════════════════════════════════

# Introspect the live source. extract_method / incremental_field / date_range
# are stamped onto EVERY discovered table identically — which is exactly what
# you want when all your tables share one increment column, and exactly what you
# do not want otherwise (use route 2 then).
data "boomi_data_integration_source_metadata" "sales" {
  count = var.create_discovery_driven_flow ? 1 : 0

  environment_id = var.environment_id
  connection_id  = boomi_data_integration_connection.mysql_source.id
  datasource     = "mysql"
  schema         = var.mysql_database # for MySQL the database IS the schema
  tables         = var.source_tables  # omit to discover every table in the schema

  # "incremental" — NOT "increment". See README.
  extract_method    = "incremental"
  incremental_field = var.incremental_field

  # date_range is ONE of three mutually exclusive incremental modes
  # (date_range / running_number / epoch). Only date_range is exposed by this
  # data source; the other two are reachable via route 2's literal JSON.
  #
  # "backfill from a date, then track forward" == time_period "custom" +
  # start_date set + end_date left unset. The platform advances the increment
  # marker after each successful run, so subsequent runs pick up where the last
  # one stopped.
  date_range = {
    time_period = "custom"
    start_date  = var.backfill_start_date
    # end_date deliberately unset: an open-ended upper bound is what makes this
    # "track forward" rather than a one-off window.
    days_back                    = 0
    include_end_value            = false
    update_increment_on_failures = false
    utc_offset                   = 0

    # Chop the backfill into chunks so the first run does not try to pull years
    # of history in one request. Irrelevant once the flow is caught up.
    split_time_intervals = {
      time_interval = var.backfill_split_interval
      interval_size = var.backfill_split_interval_size
    }
  }

  timeouts {
    # Metadata discovery is a live round-trip against the database itself. MySQL
    # is usually seconds; wide catalogues are minutes. Raise this, don't lower it.
    read = var.discovery_timeout
  }
}

locals {
  discovered_schemas = try(
    jsondecode(data.boomi_data_integration_source_metadata.sales[0].schemas_json),
    []
  )

  # merge de-duplicates on the target table's KEY columns, and the discovery
  # data source emits modified_columns without is_key — so stamp the keys on
  # from var.merge_keys (table name → key column names). With loading_method
  # "append" this whole transform is unnecessary and you can feed
  # jsondecode(...schemas_json) in directly.
  discovered_schemas_with_keys = [
    for s in local.discovered_schemas : merge(s, {
      tables = [
        for t in s.tables : merge(t, {
          details = merge(t.details, {
            modified_columns = [
              for c in try(t.details.modified_columns, []) : merge(c, {
                is_key = contains(lookup(var.merge_keys, t.details.name, []), c.name)
              })
            ]
          })
        })
      ]
    })
  ]
}

resource "boomi_data_integration_data_flow" "discovery_driven" {
  count = var.create_discovery_driven_flow ? 1 : 0

  environment_id = var.environment_id
  name           = "${var.data_flow_name_prefix}-discovered"
  description    = "MySQL → Snowflake incremental (schema discovered from the live source)"
  type           = "source_to_target"
  kind           = "main_river"

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mysql"
      connection_id = boomi_data_integration_connection.mysql_source.id
      run_type      = "multi_tables"
    }
    target  = local.target
    schemas = local.discovered_schemas_with_keys
  })

  settings_json   = jsonencode(local.settings)
  schedulers_json = jsonencode(local.schedulers)
  group_id        = var.group_id

  # `activate` on the released v1.1.0 defaults to false. On this repo's main
  # branch the default has been REMOVED: omitting it means activation is
  # unmanaged (the provider adopts whatever the server reports and never
  # activates/disables), while an explicit true/false is an enforced desired
  # state. Set it explicitly and the behaviour is identical on both — which is
  # why this example always passes a value.
  activate = var.activate
}

# ══════════════════════════════════════════════════════════════════════════════
# ROUTE 2 — HAND-WRITTEN SCHEMAS
# ══════════════════════════════════════════════════════════════════════════════

locals {
  # The literal properties.schemas[] shape. Reach for this when tables do not
  # share one increment column, when you want per-table chunk sizes, or when you
  # only want to record column DELTAS rather than every column.
  #
  # Per-table fields live under details and come from the API's
  # WriteDatabaseTableDetailsInput: name (the only required one), target_table,
  # is_selected, extract_method, incremental_field, is_custom_incremental,
  # exporter_chunk_size, table_status, modified_columns, and exactly one of
  # date_range / running_number / epoch.
  hand_written_schemas = [
    {
      name = var.mysql_database
      tables = [
        # ── Table 1: date increment on an UPDATE timestamp ─────────────────────
        {
          run_type_and_datasource = "multi_tables"
          details = {
            name                  = "customers"
            target_table          = "CUSTOMERS"
            is_selected           = true
            extract_method        = "incremental"
            incremental_field     = "updated_at"
            is_custom_incremental = false
            exporter_chunk_size   = 30000

            date_range = {
              time_period                  = "custom"
              start_date                   = var.backfill_start_date
              end_date                     = null
              days_back                    = 0
              include_end_value            = false
              split_time_intervals         = { time_interval = "days", interval_size = 7 }
              update_increment_on_failures = false
              utc_offset                   = 0
              round_up                     = null
            }

            # modified_columns records DELTAS from the source default: every
            # source column is selected unless you say otherwise. Here: mark the
            # merge key, rename one column in the target, and drop one entirely.
            modified_columns = [
              { name = "id", type = "integer", is_selected = true, is_key = true },
              { name = "email_address", type = "string", is_selected = true, alias = "EMAIL" },
              { name = "internal_notes", type = "string", is_selected = false },
            ]
          }
        },

        # ── Table 2: a DIFFERENT increment column, which is the whole reason to
        #    hand-write instead of using discovery ──────────────────────────────
        {
          run_type_and_datasource = "multi_tables"
          details = {
            name                  = "orders"
            target_table          = "ORDERS"
            is_selected           = true
            extract_method        = "incremental"
            incremental_field     = "modified_on"
            is_custom_incremental = false
            exporter_chunk_size   = 100000

            date_range = {
              time_period                  = "custom"
              start_date                   = var.backfill_start_date
              end_date                     = null
              days_back                    = 0
              include_end_value            = false
              split_time_intervals         = { time_interval = "days", interval_size = 1 }
              update_increment_on_failures = false
              utc_offset                   = 0
              round_up                     = null
            }

            modified_columns = [
              { name = "order_id", type = "integer", is_selected = true, is_key = true },
            ]
          }
        },
      ]
    },
  ]
}

resource "boomi_data_integration_data_flow" "hand_written" {
  count = var.create_hand_written_flow ? 1 : 0

  environment_id = var.environment_id
  name           = "${var.data_flow_name_prefix}-handwritten"
  description    = "MySQL → Snowflake incremental (per-table mapping written by hand)"
  type           = "source_to_target"
  kind           = "main_river"

  properties_json = jsonencode({
    properties_type = "source_to_target"
    source = {
      name          = "mysql"
      connection_id = boomi_data_integration_connection.mysql_source.id
      run_type      = "multi_tables"
    }
    target  = local.target
    schemas = local.hand_written_schemas
  })

  settings_json   = jsonencode(local.settings)
  schedulers_json = jsonencode(local.schedulers)
  group_id        = var.group_id
  activate        = var.activate
}

# ── Outputs ───────────────────────────────────────────────────────────────────

output "mysql_connection_id" {
  value = boomi_data_integration_connection.mysql_source.id
}

output "snowflake_connection_id" {
  value = boomi_data_integration_connection.snowflake_target.id
}

output "discovery_driven_data_flow_id" {
  value = one(boomi_data_integration_data_flow.discovery_driven[*].id)
}

output "hand_written_data_flow_id" {
  value = one(boomi_data_integration_data_flow.hand_written[*].id)
}

# The generated incremental mapping, for eyeballing what discovery produced
# before you trust it. Handy for diffing route 1 against route 2.
output "discovered_incremental_mapping" {
  description = "schemas_json from discovery, with merge keys stamped on."
  value       = local.discovered_schemas_with_keys
}
