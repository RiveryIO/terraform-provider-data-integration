package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccEnvironmentResource_noDescription creates an environment without the
// optional description field, then adds one (null→set). Tests the nil→set
// lifecycle for optional string attributes.
//
// Note: the set→nil direction (clearing description) is not tested here because
// the current API branch uses `description or existing_description` logic, which
// means sending null cannot clear an existing value. This should be re-enabled
// once the API supports explicit null to clear optional fields.
func TestAccEnvironmentResource_noDescription(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkEnvironmentDestroyed,
		Steps: []resource.TestStep{
			// Create without description.
			{
				Config: testAccEnvironmentMinimalConfig("tf-acc-env-nodesc"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_environment.test", "id"),
					resource.TestCheckResourceAttr("boomi_environment.test", "name", "tf-acc-env-nodesc"),
					resource.TestCheckNoResourceAttr("boomi_environment.test", "description"),
					dumpEnvironmentEntityCheck(t, "boomi_environment.test"),
				),
			},
			// Add description — must be an in-place update, not a replacement.
			{
				Config: testAccEnvironmentConfig("tf-acc-env-nodesc", "added later"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_environment.test", "description", "added later"),
					dumpEnvironmentEntityCheck(t, "boomi_environment.test"),
				),
			},
		},
	})
}

// TestAccEnvironmentResource_idempotent verifies that a second apply against
// an unchanged config produces an empty plan (no drift from the API enriching
// the response with extra fields).
func TestAccEnvironmentResource_idempotent(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkEnvironmentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentConfig("tf-acc-env-idem", "idempotency check"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_environment.test", "id"),
					dumpEnvironmentEntityCheck(t, "boomi_environment.test"),
				),
			},
			// Second apply with the same config — must produce zero diff.
			{
				Config:             testAccEnvironmentConfig("tf-acc-env-idem", "idempotency check"),
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

// testAccEnvironmentMinimalConfig creates an environment with only the required
// name field. Used for testing optional-field lifecycle.
func testAccEnvironmentMinimalConfig(name string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_environment" "test" {
  name = %q
}
`, name)
}
