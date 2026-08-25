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
	subFlowID := os.Getenv("RIVERY_ACC_SUB_FLOW_ID")
	if subFlowID == "" {
		t.Skip("RIVERY_ACC_SUB_FLOW_ID not set — a real river_id is required for a logic leaf step")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataFlowConfig("tf-acc-flow", "first", subFlowID),
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
				Config: testAccDataFlowConfig("tf-acc-flow", "second", subFlowID),
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
// Requires a real CDC data flow id in DATA_INTEGRATION_ACC_CDC_DATA_FLOW_ID;
// skipped otherwise (the set path works on any data flow, but a meaningful test
// wants a CDC data flow). config_json is config-authoritative, so there is no
// import-verify step.
func TestAccCDCConfigResource(t *testing.T) {
	dataFlowID := os.Getenv("DATA_INTEGRATION_ACC_CDC_DATA_FLOW_ID")
	if dataFlowID == "" {
		t.Skip("DATA_INTEGRATION_ACC_CDC_DATA_FLOW_ID not set — a CDC data flow id is required")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccCDCConfigConfig(dataFlowID, "515820321"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_data_integration_data_flow_cdc_config.test", "id", dataFlowID),
					resource.TestCheckResourceAttr("boomi_data_integration_data_flow_cdc_config.test", "data_flow_id", dataFlowID),
				),
			},
			{
				Config: testAccCDCConfigConfig(dataFlowID, "515820999"),
				Check:  resource.TestCheckResourceAttrSet("boomi_data_integration_data_flow_cdc_config.test", "config_json"),
			},
		},
	})
}

func testAccCDCConfigConfig(dataFlowID, pos string) string {
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
`, dataFlowID, pos)
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

func testAccDataFlowConfig(name, desc, subFlowID string) string {
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
`, name, desc, subFlowID)
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

// TestAccBlueprintResource exercises create → import → update for a blueprint
// (recipe) and its backing blueprint_file, asserting idempotency at each step.
// Unlike logicode_file, both APIs support PUT, so file content and the
// blueprint's description can both be updated in place without forcing a new
// resource.
func TestAccBlueprintResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBlueprintConfig("tf-acc-blueprint", "first"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_data_integration_blueprint_file.test", "id"),
					resource.TestCheckResourceAttrSet("boomi_data_integration_blueprint.test", "id"),
					resource.TestCheckResourceAttr("boomi_data_integration_blueprint.test", "name", "tf-acc-blueprint"),
					resource.TestCheckResourceAttr("boomi_data_integration_blueprint.test", "description", "first"),
					resource.TestCheckResourceAttrPair(
						"boomi_data_integration_blueprint.test", "file_cross_id",
						"boomi_data_integration_blueprint_file.test", "id"),
				),
			},
			{
				ResourceName:      "boomi_data_integration_blueprint.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: testAccBlueprintConfig("tf-acc-blueprint", "second"),
				Check: resource.TestCheckResourceAttr(
					"boomi_data_integration_blueprint.test", "description", "second"),
			},
		},
	})
}

// TestAccConnectionTypeDataSource reads a real connection type's property
// schema from the live API and asserts the deprecated properties_json alias
// still carries the identical value as property_schema_json. The unit tests
// cover the same contract against a mock server; this one proves the live API
// response actually populates both.
func TestAccConnectionTypeDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
provider "boomi" {}

data "boomi_data_integration_connection_type" "test" {
  connection_type = "mysql"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(
						"data.boomi_data_integration_connection_type.test", "connection_type", "mysql"),
					resource.TestCheckResourceAttrSet(
						"data.boomi_data_integration_connection_type.test", "connection_type_name"),
					resource.TestCheckResourceAttrSet(
						"data.boomi_data_integration_connection_type.test", "property_schema_json"),
					// The backwards-compatibility promise: same value, both names.
					resource.TestCheckResourceAttrPair(
						"data.boomi_data_integration_connection_type.test", "property_schema_json",
						"data.boomi_data_integration_connection_type.test", "properties_json"),
					// A real mysql type exposes host among its properties.
					resource.TestCheckTypeSetElemAttr(
						"data.boomi_data_integration_connection_type.test", "property_names.*", "host"),
				),
			},
		},
	})
}

func testAccBlueprintConfig(name, desc string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_integration_blueprint_file" "test" {
  filename = "tf-acc-blueprint.yaml"
  content  = <<-YAML
    name: tf-acc-blueprint
    steps: []
  YAML
}

resource "boomi_data_integration_blueprint" "test" {
  name          = %q
  description   = %q
  file_cross_id = boomi_data_integration_blueprint_file.test.id
}
`, name, desc)
}
