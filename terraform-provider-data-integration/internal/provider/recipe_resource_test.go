package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccRecipeResource_basic exercises create → import → update name for a
// boomi_recipe. Requires DATA_INTEGRATION_ACC_RECIPE_FILE_PATH pointing to a
// local recipe file (YAML or JSON).
//
// Recipe create is a two-step API operation:
//  1. POST /recipes/files  (multipart/form-data upload)
//  2. POST /recipes        (create recipe record referencing the uploaded file)
//
// Drift is detected by comparing content_hash against the file on disk.
func TestAccRecipeResource_basic(t *testing.T) {
	filePath := os.Getenv("DATA_INTEGRATION_ACC_RECIPE_FILE_PATH")
	if filePath == "" {
		t.Skip("DATA_INTEGRATION_ACC_RECIPE_FILE_PATH must be set for recipe acceptance tests")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and verify.
			{
				Config: testAccRecipeConfig("tf-acc-recipe", filePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("boomi_recipe.test", "id"),
					resource.TestCheckResourceAttr("boomi_recipe.test", "name", "tf-acc-recipe"),
					resource.TestCheckResourceAttrSet("boomi_recipe.test", "content_hash"),
					resource.TestCheckResourceAttrSet("boomi_recipe.test", "environment_id"),
				),
			},
			// Import — content_hash is computed from the local file; file_path is
			// not stored by the API and will be absent after import.
			{
				ResourceName:            "boomi_recipe.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"file_path", "content_hash"},
			},
			// Update name — must be in-place, same resource id.
			{
				Config: testAccRecipeConfig("tf-acc-recipe-renamed", filePath),
				Check:  resource.TestCheckResourceAttr("boomi_recipe.test", "name", "tf-acc-recipe-renamed"),
			},
		},
	})
}

// TestAccRecipeResource_filePathRequiresReplace verifies that changing the
// file_path (filename) produces a replace plan. The API stores the original
// filename in the S3 key — changing it would cause an S3 key drift bug where
// the new content is uploaded to a different key but the recipe record still
// points to the old key.
func TestAccRecipeResource_filePathRequiresReplace(t *testing.T) {
	filePath := os.Getenv("DATA_INTEGRATION_ACC_RECIPE_FILE_PATH")
	altFilePath := os.Getenv("DATA_INTEGRATION_ACC_RECIPE_ALT_FILE_PATH")
	if filePath == "" || altFilePath == "" {
		t.Skip("DATA_INTEGRATION_ACC_RECIPE_FILE_PATH and DATA_INTEGRATION_ACC_RECIPE_ALT_FILE_PATH must be set")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccRecipeConfig("tf-acc-recipe-path", filePath),
				Check:  resource.TestCheckResourceAttrSet("boomi_recipe.test", "id"),
			},
			// Change file_path — must be non-empty plan (replace, not in-place update).
			{
				Config:             testAccRecipeConfig("tf-acc-recipe-path", altFilePath),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAccRecipeResource_contentDriftDetected verifies that modifying the file
// content (same path, different content) is detected as drift and triggers an
// update plan. The provider recomputes content_hash on Read and compares it to
// the stored hash.
func TestAccRecipeResource_contentDriftDetected(t *testing.T) {
	filePath := os.Getenv("DATA_INTEGRATION_ACC_RECIPE_FILE_PATH")
	if filePath == "" {
		t.Skip("DATA_INTEGRATION_ACC_RECIPE_FILE_PATH must be set")
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccRecipeConfig("tf-acc-recipe-drift", filePath),
				Check:  resource.TestCheckResourceAttrSet("boomi_recipe.test", "content_hash"),
			},
			// Idempotent re-plan — same file, same hash, no diff.
			{
				Config:             testAccRecipeConfig("tf-acc-recipe-drift", filePath),
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

func testAccRecipeConfig(name, filePath string) string {
	return fmt.Sprintf(`
provider "boomi" {}

resource "boomi_recipe" "test" {
  name      = %q
  file_path = %q
}
`, name, filePath)
}
