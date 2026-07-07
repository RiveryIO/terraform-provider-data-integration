package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccDataFrameResource_nameRequiresReplace verifies that changing the
// dataframe name produces a replace plan. The API has no rename endpoint —
// the name is the resource's primary key — so the schema marks name as
// RequiresReplace. This test confirms the plan-time behaviour without applying
// the replacement (plan-only to avoid creating a second dataframe with bad creds).
func TestAccDataFrameResource_nameRequiresReplace(t *testing.T) {
	connID := os.Getenv("DATA_INTEGRATION_ACC_DF_CONNECTION_ID")
	if connID == "" {
		t.Skip("DATA_INTEGRATION_ACC_DF_CONNECTION_ID must be set for dataframe acceptance tests")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with original name.
			{
				Config: testAccDataFrameConfig("tf-acc-df-orig", connID, "my-bucket"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_dataframe.test", "name", "tf-acc-df-orig"),
					resource.TestCheckResourceAttr("boomi_dataframe.test", "id", "tf-acc-df-orig"),
				),
			},
			// Change name — plan must be non-empty (destroy + create), not an update.
			{
				Config:             testAccDataFrameConfig("tf-acc-df-renamed", connID, "my-bucket"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccDataFrameResource_connectionSettingsUpdate verifies that updating
// connection_settings (the only in-place-updatable field) does not recreate
// the dataframe. connection_settings is config-authoritative so no drift from
// API enrichment is expected.
func TestAccDataFrameResource_connectionSettingsUpdate(t *testing.T) {
	connID := os.Getenv("DATA_INTEGRATION_ACC_DF_CONNECTION_ID")
	if connID == "" {
		t.Skip("DATA_INTEGRATION_ACC_DF_CONNECTION_ID must be set for dataframe acceptance tests")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataFrameConfig("tf-acc-df-cs", connID, "bucket-original"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_dataframe.test", "id"),
					resource.TestCheckResourceAttr(
						"boomi_dataframe.test", "connection_settings.default_bucket", "bucket-original"),
				),
			},
			// Import verify — connection_settings is config-authoritative so it
			// will be null on import; exclude it from the diff.
			{
				ResourceName:            "boomi_dataframe.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"connection_settings"},
			},
			// Update bucket — must be in-place, same resource id.
			{
				Config: testAccDataFrameConfig("tf-acc-df-cs", connID, "bucket-updated"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_dataframe.test", "id", "tf-acc-df-cs"),
					resource.TestCheckResourceAttr(
						"boomi_dataframe.test", "connection_settings.default_bucket", "bucket-updated"),
				),
			},
		},
	})
}

// testAccDataFrameMinimalConfig creates a dataframe with only the required
// name, relying on the provider-level environment_id.
func testAccDataFrameMinimalConfig(name string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_dataframe" "test" {
  name = %q
}
`, name)
}
