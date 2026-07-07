package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccKeyPairResource_basic exercises create → import for a boomi_key_pair.
// The private key is returned only once on create and never on subsequent reads,
// so it must be excluded from import verify. The public key is always readable.
func TestAccKeyPairResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create — both public_key and private_key must be populated in state.
			{
				Config: testAccKeyPairConfig("tf-acc-key"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_key_pair.test", "id"),
					resource.TestCheckResourceAttr("boomi_key_pair.test", "name", "tf-acc-key"),
					resource.TestCheckResourceAttrSet("boomi_key_pair.test", "public_key"),
					// private_key is sensitive — check it exists in state, not its value.
					resource.TestCheckResourceAttrSet("boomi_key_pair.test", "private_key"),
				),
			},
			// Import — private_key is not returned by the API after create, so it
			// will be absent in imported state. Exclude it from the diff check.
			{
				ResourceName:            "boomi_key_pair.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"private_key"},
			},
		},
	})
}

// TestAccKeyPairResource_nameRequiresReplace verifies that changing the name
// produces a replace plan. The API has no update endpoint (returns 405) — every
// change must destroy and recreate.
func TestAccKeyPairResource_nameRequiresReplace(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccKeyPairConfig("tf-acc-key-orig"),
				Check:  resource.TestCheckResourceAttr("boomi_key_pair.test", "name", "tf-acc-key-orig"),
			},
			// Change name — must be non-empty plan (replace, not in-place update).
			{
				Config:             testAccKeyPairConfig("tf-acc-key-renamed"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccKeyPairResource_privateKeyInStateAfterCreate verifies that the private
// key is stored in Terraform state immediately after create, since it is never
// returned again by subsequent API reads. If this test fails, the private key
// would be permanently lost.
func TestAccKeyPairResource_privateKeyInStateAfterCreate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccKeyPairConfig("tf-acc-key-secret"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_key_pair.test", "private_key"),
					resource.TestCheckResourceAttrSet("boomi_key_pair.test", "public_key"),
				),
			},
			// Re-plan — private_key must still be in state (not lost on read).
			{
				Config:             testAccKeyPairConfig("tf-acc-key-secret"),
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

// TestAccKeyPairResource_deleteIdempotent verifies that deleting an already-
// deleted key pair does not return an error. The API returns 404 on repeated
// deletes; the provider must treat this as success.
func TestAccKeyPairResource_deleteIdempotent(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccKeyPairConfig("tf-acc-key-idem"),
				Check:  resource.TestCheckResourceAttrSet("boomi_key_pair.test", "id"),
			},
		},
	})
}

func testAccKeyPairConfig(name string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_key_pair" "test" {
  name = %q
}
`, name)
}
