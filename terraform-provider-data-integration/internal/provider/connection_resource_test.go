package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccConnectionResource_basic exercises create → import → update name for a
// boomi_connection. Set DATA_INTEGRATION_ACC_CONN_TYPE and
// DATA_INTEGRATION_ACC_CONN_PARAMS to supply real credentials for the chosen
// connection type. parameters_json is excluded from import verify because the
// API never returns credentials.
func TestAccConnectionResource_basic(t *testing.T) {
	connType := os.Getenv("DATA_INTEGRATION_ACC_CONN_TYPE")
	connParams := os.Getenv("DATA_INTEGRATION_ACC_CONN_PARAMS")
	if connType == "" || connParams == "" {
		t.Skip("DATA_INTEGRATION_ACC_CONN_TYPE and DATA_INTEGRATION_ACC_CONN_PARAMS must be set for connection acceptance tests")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and verify
			{
				Config: testAccConnectionConfig("tf-acc-conn", connType, connParams),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_connection.test", "id"),
					resource.TestCheckResourceAttr("boomi_connection.test", "name", "tf-acc-conn"),
					resource.TestCheckResourceAttr("boomi_connection.test", "type", connType),
					resource.TestCheckResourceAttrSet("boomi_connection.test", "environment_id"),
				),
			},
			// Import — parameters_json is not returned by the API so it will be
			// null in the imported state; exclude it from the diff check.
			{
				ResourceName:            "boomi_connection.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"parameters_json"},
			},
			// Update name — type stays unchanged so no replacement occurs.
			{
				Config: testAccConnectionConfig("tf-acc-conn-renamed", connType, connParams),
				Check:  resource.TestCheckResourceAttr("boomi_connection.test", "name", "tf-acc-conn-renamed"),
			},
		},
	})
}

// TestAccConnectionResource_typeRequiresReplace asserts that changing the
// connection type generates a non-empty plan. The API rejects in-place type
// changes with a 400, so the resource marks type RequiresReplace. This step
// runs plan-only to avoid trying to apply a connection with bad credentials.
func TestAccConnectionResource_typeRequiresReplace(t *testing.T) {
	connType := os.Getenv("DATA_INTEGRATION_ACC_CONN_TYPE")
	connParams := os.Getenv("DATA_INTEGRATION_ACC_CONN_PARAMS")
	if connType == "" || connParams == "" {
		t.Skip("DATA_INTEGRATION_ACC_CONN_TYPE and DATA_INTEGRATION_ACC_CONN_PARAMS must be set")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with the real type.
			{
				Config: testAccConnectionConfig("tf-acc-conn-type", connType, connParams),
				Check:  resource.TestCheckResourceAttr("boomi_connection.test", "type", connType),
			},
			// Change the type — the plan should be non-empty (destroy + create),
			// not an in-place update.
			{
				Config:             testAccConnectionConfig("tf-acc-conn-type", connType+"_changed", `{}`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}


func testAccConnectionConfig(name, connType, paramsJSON string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_connection" "test" {
  name            = %q
  type            = %q
  parameters_json = %q
}
`, name, connType, paramsJSON)
}
