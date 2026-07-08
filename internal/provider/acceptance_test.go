package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// testAccPreCheck verifies the integration credentials are present. Acceptance
// tests (resource.Test) auto-skip unless TF_ACC is set; this adds a clear
// failure when TF_ACC is set but creds are missing.
func testAccPreCheck(t *testing.T) {
	for _, k := range []string{
		"DATA_INTEGRATION_API_TOKEN",
		"DATA_INTEGRATION_ACCOUNT_ID",
		"DATA_INTEGRATION_ENVIRONMENT_ID",
	} {
		if os.Getenv(k) == "" {
			t.Fatalf("%s must be set for TF_ACC acceptance tests", k)
		}
	}
}

// TestAccEnvironmentResource exercises create → import → update against a live
// integration account, asserting idempotency (no plan diff) at each step.
func TestAccEnvironmentResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentConfig("tf-acc-env", "managed by terraform-provider-data-integration"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_data_integration_environment.test", "id"),
					resource.TestCheckResourceAttr("boomi_data_integration_environment.test", "name", "tf-acc-env"),
				),
			},
			{
				ResourceName:      "boomi_data_integration_environment.test",
				ImportState:       true,
				ImportStateVerify: true, // imported state must plan clean
			},
			{
				Config: testAccEnvironmentConfig("tf-acc-env-renamed", "updated description"),
				Check: resource.TestCheckResourceAttr(
					"boomi_data_integration_environment.test", "name", "tf-acc-env-renamed"),
			},
		},
	})
}

// TestAccDataFlowResource exercises create → import → update for a logic data
// flow, the resource where read shape ≠ write shape — the import-verify step is
// the load-bearing check that normalization keeps plans clean.
func TestAccDataFlowResource(t *testing.T) {
	subRiverID := os.Getenv("RIVERY_ACC_SUBRIVER_ID")
	if subRiverID == "" {
		t.Skip("RIVERY_ACC_SUBRIVER_ID not set — a real river_id is required for a logic leaf step")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataFlowConfig("tf-acc-flow", "first", subRiverID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_data_integration_data_flow.test", "id"),
					resource.TestCheckResourceAttr("boomi_data_integration_data_flow.test", "name", "tf-acc-flow"),
					resource.TestCheckResourceAttr("boomi_data_integration_data_flow.test", "description", "first"),
				),
			},
			{
				ResourceName:      "boomi_data_integration_data_flow.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					// properties_json/settings_json are config-authoritative: on import
					// they are populated from the server's *enriched* shape, which
					// differs from the minimal configured value. Reconcile config to
					// the server shape after import for a clean plan.
					"properties_json",
					"settings_json",
				},
			},
			{
				Config: testAccDataFlowConfig("tf-acc-flow", "second", subRiverID),
				Check: resource.TestCheckResourceAttr(
					"boomi_data_integration_data_flow.test", "description", "second"),
			},
		},
	})
}

// TestAccDataFrameResource exercises create → import → update for a dataframe.
// Dataframes are keyed by name (no cross_id) and reference a storage connection
// via connection_settings — the import-verify step asserts the name-as-id and
// nested-block handling keep plans clean.
func TestAccDataFrameResource(t *testing.T) {
	connID := os.Getenv("DATA_INTEGRATION_ACC_DF_CONNECTION_ID")
	if connID == "" {
		t.Skip("DATA_INTEGRATION_ACC_DF_CONNECTION_ID not set — a real storage connection id is required")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataFrameConfig("tf-acc-df", connID, "rivery-dev-tests"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_data_integration_dataframe.test", "id", "tf-acc-df"),
					resource.TestCheckResourceAttr("boomi_data_integration_dataframe.test", "name", "tf-acc-df"),
					resource.TestCheckResourceAttr("boomi_data_integration_dataframe.test", "connection_settings.connection", connID),
				),
			},
			{
				ResourceName:      "boomi_data_integration_dataframe.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					// connection_settings is config-authoritative: an imported resource
					// carries no nested block until reconciled from config.
					"connection_settings",
				},
			},
			{
				Config: testAccDataFrameConfig("tf-acc-df", connID, "rivery-dev-tests-2"),
				Check: resource.TestCheckResourceAttr(
					"boomi_data_integration_dataframe.test", "connection_settings.default_bucket", "rivery-dev-tests-2"),
			},
		},
	})
}

// TestAccVariableResource exercises create → import → update for an environment
// variable (env-scoped key/value, merge-on-write). Requires a token carrying the
// variables:list/edit scopes (role:admin alone is insufficient).
func TestAccVariableResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccVariableConfig("tf_acc_var", "first"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_data_integration_variable.test", "id", "tf_acc_var"),
					resource.TestCheckResourceAttr("boomi_data_integration_variable.test", "key", "tf_acc_var"),
					resource.TestCheckResourceAttr("boomi_data_integration_variable.test", "value", "first"),
				),
			},
			{
				ResourceName:      "boomi_data_integration_variable.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: testAccVariableConfig("tf_acc_var", "second"),
				Check:  resource.TestCheckResourceAttr("boomi_data_integration_variable.test", "value", "second"),
			},
		},
	})
}

func testAccVariableConfig(key, value string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_integration_variable" "test" {
  key   = %q
  value = %q
}
`, key, value)
}

// TestAccCDCConfigResource exercises create → update → destroy of a CDC offset.
// Requires a real CDC river id in DATA_INTEGRATION_ACC_CDC_RIVER_ID; skipped
// otherwise (the set path works on any river, but a meaningful test wants a CDC
// river). config_json is config-authoritative, so there is no import-verify step.
func TestAccCDCConfigResource(t *testing.T) {
	riverID := os.Getenv("DATA_INTEGRATION_ACC_CDC_RIVER_ID")
	if riverID == "" {
		t.Skip("DATA_INTEGRATION_ACC_CDC_RIVER_ID not set — a CDC river id is required")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccCDCConfigConfig(riverID, "515820321"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_data_integration_data_flow_cdc_config.test", "id", riverID),
					resource.TestCheckResourceAttr("boomi_data_integration_data_flow_cdc_config.test", "data_flow_id", riverID),
				),
			},
			{
				Config: testAccCDCConfigConfig(riverID, "515820999"),
				Check:  resource.TestCheckResourceAttrSet("boomi_data_integration_data_flow_cdc_config.test", "config_json"),
			},
		},
	})
}

func testAccCDCConfigConfig(riverID, pos string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_integration_data_flow_cdc_config" "test" {
  data_flow_id = %q
  config_json = jsonencode({
    datasource_type = "mysql"
    binlog_file     = "mysql-bin-changelog.000931"
    binlog_position = %q
  })
}
`, riverID, pos)
}

func testAccEnvironmentConfig(name, desc string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_integration_environment" "test" {
  name        = %q
  description = %q
}
`, name, desc)
}

func testAccDataFlowConfig(name, desc, subRiverID string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_integration_data_flow" "test" {
  name        = %q
  description = %q
  type        = "logic"
  properties_json = jsonencode({
    properties_type = "logic"
    logic_steps = [{
      type            = "river"
      name            = "step-1"
      river_id        = %q
      input_variables = {}
    }]
  })
}
`, name, desc, subRiverID)
}

func testAccDataFrameConfig(name, connID, bucket string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_integration_dataframe" "test" {
  name = %q
  connection_settings = {
    connection     = %q
    datasource_id  = "aws"
    storage_type   = "s3"
    default_bucket = %q
  }
}
`, name, connID, bucket)
}
