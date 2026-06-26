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
				Config: testAccEnvironmentConfig("tf-acc-env", "managed by terraform-provider-rivery"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("rivery_environment.test", "id"),
					resource.TestCheckResourceAttr("rivery_environment.test", "name", "tf-acc-env"),
				),
			},
			{
				ResourceName:      "rivery_environment.test",
				ImportState:       true,
				ImportStateVerify: true, // imported state must plan clean
			},
			{
				Config: testAccEnvironmentConfig("tf-acc-env-renamed", "updated description"),
				Check: resource.TestCheckResourceAttr(
					"rivery_environment.test", "name", "tf-acc-env-renamed"),
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
					resource.TestCheckResourceAttrSet("rivery_data_flow.test", "id"),
					resource.TestCheckResourceAttr("rivery_data_flow.test", "name", "tf-acc-flow"),
					resource.TestCheckResourceAttr("rivery_data_flow.test", "description", "first"),
				),
			},
			{
				ResourceName:      "rivery_data_flow.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					// settings_json has a server-side default that may not be set on import
					"settings_json",
				},
			},
			{
				Config: testAccDataFlowConfig("tf-acc-flow", "second", subRiverID),
				Check: resource.TestCheckResourceAttr(
					"rivery_data_flow.test", "description", "second"),
			},
		},
	})
}

func testAccEnvironmentConfig(name, desc string) string {
	return fmt.Sprintf(`
provider "rivery" {}

resource "rivery_environment" "test" {
  name        = %q
  description = %q
}
`, name, desc)
}

func testAccDataFlowConfig(name, desc, subRiverID string) string {
	return fmt.Sprintf(`
provider "rivery" {}

resource "rivery_data_flow" "test" {
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
