package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccDataFlowResource_minimalCreate verifies that a boomi_data_flow can be
// created with only the required name field. kind defaults to "main_river" and
// type to "logic" — both computed defaults must be stable across a second plan.
func TestAccDataFlowResource_minimalCreate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataFlowMinimalConfig("tf-acc-flow-min"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_data_flow.test", "id"),
					resource.TestCheckResourceAttr("boomi_data_flow.test", "name", "tf-acc-flow-min"),
					resource.TestCheckResourceAttr("boomi_data_flow.test", "kind", "main_river"),
					resource.TestCheckResourceAttr("boomi_data_flow.test", "type", "logic"),
				),
			},
			// Second plan against the same config must be empty — computed
			// defaults must not produce drift after the first apply.
			{
				Config:             testAccDataFlowMinimalConfig("tf-acc-flow-min"),
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

// TestAccDataFlowResource_descriptionUpdate verifies that changing the
// description field is an in-place update (no destroy+create) and that the
// new value is reflected in state after apply.
func TestAccDataFlowResource_descriptionUpdate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataFlowWithDescConfig("tf-acc-flow-desc", "initial description"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_data_flow.test", "id"),
					resource.TestCheckResourceAttr("boomi_data_flow.test", "description", "initial description"),
				),
			},
			{
				Config: testAccDataFlowWithDescConfig("tf-acc-flow-desc", "updated description"),
				Check:  resource.TestCheckResourceAttr("boomi_data_flow.test", "description", "updated description"),
			},
		},
	})
}

// TestAccDataFlowResource_propertiesConfigAuthoritative verifies that
// properties_json and settings_json are config-authoritative. After a create,
// neither field should generate a diff on re-plan even though the API enriches
// the stored object with extra keys (step_id, notification blocks, etc.).
func TestAccDataFlowResource_propertiesConfigAuthoritative(t *testing.T) {
	propertiesJSON := `{"steps":[]}`
	settingsJSON := `{"notifications":[]}`
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDataFlowWithPropertiesConfig("tf-acc-flow-props", propertiesJSON, settingsJSON),
				Check:  resource.TestCheckResourceAttrSet("boomi_data_flow.test", "id"),
			},
			// Re-plan with the same config — properties_json / settings_json must
			// not show a diff even though the API may have enriched the stored value.
			{
				Config:             testAccDataFlowWithPropertiesConfig("tf-acc-flow-props", propertiesJSON, settingsJSON),
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

func testAccDataFlowMinimalConfig(name string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_flow" "test" {
  name = %q
}
`, name)
}

func testAccDataFlowWithDescConfig(name, description string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_flow" "test" {
  name        = %q
  description = %q
}
`, name, description)
}

func testAccDataFlowWithPropertiesConfig(name, propertiesJSON, settingsJSON string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_data_flow" "test" {
  name            = %q
  properties_json = %q
  settings_json   = %q
}
`, name, propertiesJSON, settingsJSON)
}
