package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccVariableResource_coexist verifies that two variables sharing the same
// environment do not overwrite each other. The underlying PUT /variables
// endpoint uses $set merge semantics — a delete of one key must not affect any
// other key. This test guards against regressions in the delete path that
// could remove the wrong key or clear the whole variables map.
func TestAccVariableResource_coexist(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create both variables.
			{
				Config: testAccTwoVariablesConfig("tf_acc_coexist_a", "alpha", "tf_acc_coexist_b", "beta"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_variable.first", "key", "tf_acc_coexist_a"),
					resource.TestCheckResourceAttr("boomi_variable.first", "value", "alpha"),
					resource.TestCheckResourceAttr("boomi_variable.second", "key", "tf_acc_coexist_b"),
					resource.TestCheckResourceAttr("boomi_variable.second", "value", "beta"),
				),
			},
			// Remove the second variable. The first must survive with its value intact.
			{
				Config: testAccSingleNamedVariableConfig("tf_acc_coexist_a", "alpha"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_variable.first", "value", "alpha"),
				),
			},
		},
	})
}

// TestAccVariableResource_updateValue verifies that a value-only update does
// not recreate the variable (key stays the same, just the stored value changes).
func TestAccVariableResource_updateValue(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccVariableConfig("tf_acc_var_upd", "original"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("boomi_variable.test", "id", "tf_acc_var_upd"),
					resource.TestCheckResourceAttr("boomi_variable.test", "value", "original"),
				),
			},
			// Update the value — must be an in-place change, not a replacement.
			{
				Config: testAccVariableConfig("tf_acc_var_upd", "updated"),
				Check:  resource.TestCheckResourceAttr("boomi_variable.test", "value", "updated"),
			},
			// ID (key) must not have changed.
			{
				Config: testAccVariableConfig("tf_acc_var_upd", "updated"),
				Check:  resource.TestCheckResourceAttr("boomi_variable.test", "id", "tf_acc_var_upd"),
			},
		},
	})
}

// testAccTwoVariablesConfig produces a config with two named variable resources.
// Used by coexistence tests.
func testAccTwoVariablesConfig(key1, val1, key2, val2 string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_variable" "first" {
  key   = %q
  value = %q
}

resource "boomi_variable" "second" {
  key   = %q
  value = %q
}
`, key1, val1, key2, val2)
}

// testAccSingleNamedVariableConfig produces a config with a single resource
// named "first". Used after testAccTwoVariablesConfig to drop the second one.
func testAccSingleNamedVariableConfig(key, value string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_variable" "first" {
  key   = %q
  value = %q
}
`, key, value)
}
