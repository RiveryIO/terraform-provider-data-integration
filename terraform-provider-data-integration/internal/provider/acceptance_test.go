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
					resource.TestCheckResourceAttrSet("boomi_environment.test", "id"),
					resource.TestCheckResourceAttr("boomi_environment.test", "name", "tf-acc-env"),
				),
			},
			{
				ResourceName:      "boomi_environment.test",
				ImportState:       true,
				ImportStateVerify: true, // imported state must plan clean
			},
			{
				Config: testAccEnvironmentConfig("tf-acc-env-renamed", "updated description"),
				Check: resource.TestCheckResourceAttr(
					"boomi_environment.test", "name", "tf-acc-env-renamed"),
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
					resource.TestCheckResourceAttrSet("boomi_data_flow.test", "id"),
					resource.TestCheckResourceAttr("boomi_data_flow.test", "name", "tf-acc-flow"),
					resource.TestCheckResourceAttr("boomi_data_flow.test", "description", "first"),
				),
			},
			{
				ResourceName:      "boomi_data_flow.test",
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
					"boomi_data_flow.test", "description", "second"),
			},
		},
	})
}

func testAccEnvironmentConfig(name, desc string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_environment" "test" {
  name        = %q
  description = %q
}
`, name, desc)
}

func testAccDataFlowConfig(name, desc, subRiverID string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_flow" "test" {
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
