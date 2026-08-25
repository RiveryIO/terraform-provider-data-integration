# Facebook Ads → Snowflake
#
# Syncs a Facebook Ads predefined report (ad account level) into Snowflake
# using merge loading.
#
# Facebook Ads connections authenticate via OAuth — authorise the connection
# in the Boomi Data Integration UI first, then import it into Terraform state.

# ── Connections ───────────────────────────────────────────────────────────────

import {
  to = boomi_data_integration_connection.facebook_ads
  id = "<facebook-ads-connection-cross-id>"
}

resource "boomi_data_integration_connection" "facebook_ads" {
  name = "Facebook Ads"
  type = "fb"
}

# ── Data flow ─────────────────────────────────────────────────────────────────

resource "boomi_data_integration_data_flow" "facebook_ads_to_snowflake" {
  name     = "Facebook Ads → Snowflake"
  kind     = "main_river"
  type     = "source_to_target"
  activate = true

  schedule = {
    cron_expression = "0 0 * * *" # daily at midnight
    is_enabled      = true
  }

  properties_json = jsonencode({
    properties_type = "source_to_target"

    source = {
      name          = "facebook_ads"
      run_type      = "predefined_report"
      connection_id = boomi_data_integration_connection.facebook_ads.id
      additional_settings = {
        report          = "insight_report"
        data_level      = "ad_account"
        extract_method  = "all"
        connection_type = "fb"
      }
    }

    target = {
      name           = "snowflake"
      connection_id  = boomi_data_integration_connection.snowflake.id
      loading_method = "merge"
      merge_method   = "merge"
      database_name  = "ANALYTICS"
      schema_name    = "PUBLIC"
      target_prefix  = "fb_"
    }

    schemas = [{
      name = "no_schema"
      tables = [{
        run_type_and_datasource = "predefined_report"
        details = {
          table_name     = "predefined_ad_account"
          target_table   = "fb_ads_ad_account"
          is_selected    = true
          extract_method = "all"
        }
      }]
    }]
  })
}
