terraform {
  required_providers {
    boomi = {
      source  = "riveryio/data-integration"
      version = "~> 2.0"
    }
  }
}

# Credentials via environment variables:
#   DATA_INTEGRATION_API_TOKEN
#   DATA_INTEGRATION_ACCOUNT_ID
#   DATA_INTEGRATION_ENVIRONMENT_ID
provider "boomi" {}
