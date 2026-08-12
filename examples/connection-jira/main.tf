resource "boomi_data_integration_connection" "jira" {
  name = "Jira"
  type = "jira"

  # Property names come from GET /v1/connections_types/jira. There is no
  # `base_url` and no `password` on this connector: credentials_type = "token"
  # requires domain_name + username + api_token. (The "server_app" arm uses
  # full_url / username_server_app / password_server_app instead.)
  #
  # The connections API silently drops unknown parameters_json keys, so a wrong
  # field name applies cleanly and leaves you with a connection that has no
  # credential — check api_token_exists on GET .../connections/<id>.
  parameters_json = jsonencode({
    credentials_type = "token"
    domain_name      = "yourorg" # bare subdomain — not https://yourorg.atlassian.net
    username         = "user@example.com"
    api_token        = "..." # Atlassian API token — Settings → Security → API Tokens
  })
}
