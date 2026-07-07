package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// testAccClientFromEnv builds a client from the same env vars the provider
// reads, so helpers and sweepers share auth with the running test suite.
// When DATA_INTEGRATION_DEBUG=1 the client will also log every HTTP round-trip.
func testAccClientFromEnv(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New(client.Config{
		BaseURL:   envOrDefault("DATA_INTEGRATION_API_URL", "https://api.rivery.io"),
		Token:     os.Getenv("DATA_INTEGRATION_API_TOKEN"),
		AccountID: os.Getenv("DATA_INTEGRATION_ACCOUNT_ID"),
	})
	if err != nil {
		t.Fatalf("building test client: %v", err)
	}
	return c
}

// dumpEnvironmentEntityCheck is a resource.TestCheckFunc that GETs the
// environment from the API and logs the full raw document with t.Logf.
//
// Why "entity in mongo": the API reads directly from MongoDB and returns the
// full document including server-managed fields the provider never touches
// (is_deleted, is_deleting, account_id, created_at, group_name, …). This makes
// it possible to see exact MongoDB state without a separate pymongo query.
//
// Activate: pass it to resource.ComposeAggregateTestCheckFunc in a step Check,
// then run the test with -v to see the t.Logf output:
//
//	Check: resource.ComposeAggregateTestCheckFunc(
//	    resource.TestCheckResourceAttrSet("boomi_environment.test", "id"),
//	    dumpEnvironmentEntityCheck(t, "boomi_environment.test"),
//	),
//
// Set DATA_INTEGRATION_DEBUG=1 to also see the HTTP request/response pair.
func dumpEnvironmentEntityCheck(t *testing.T, resourceAddr string) resource.TestCheckFunc {
	t.Helper()
	return func(s *terraform.State) error {
		ms := s.RootModule()
		rs, ok := ms.Resources[resourceAddr]
		if !ok {
			return fmt.Errorf("resource %q not in state", resourceAddr)
		}
		id := rs.Primary.ID
		if id == "" {
			return fmt.Errorf("resource %q has empty primary ID", resourceAddr)
		}

		entity, err := testAccClientFromEnv(t).GetEnvironment(context.Background(), id)
		if err != nil {
			t.Logf("[ENTITY] %s id=%s → GET error: %v", resourceAddr, id, err)
			return nil // informational: don't fail the test itself
		}
		raw, _ := json.MarshalIndent(entity, "", "  ")
		t.Logf("[ENTITY] %s id=%s (API/MongoDB document):\n%s", resourceAddr, id, string(raw))
		return nil
	}
}

// checkEnvironmentDestroyed is the CheckDestroy hook for environment TestCases.
// It runs after the final terraform destroy and asserts that every
// boomi_environment in the test state is truly gone: 404 or is_deleted=true.
//
// Without this, a silent swallow in Delete() (e.g. the 409 "already deleting"
// guard) could let the test pass while leaving a live environment in the account.
// Combined with TestAccSweepEnvironments, this covers two failure modes:
//   - CheckDestroy: catches quiet Delete() bugs when tests run normally.
//   - Sweeper: cleans up orphaned state when tests crash mid-run.
func checkEnvironmentDestroyed(s *terraform.State) error {
	c, err := client.New(client.Config{
		BaseURL:   envOrDefault("DATA_INTEGRATION_API_URL", "https://api.rivery.io"),
		Token:     os.Getenv("DATA_INTEGRATION_API_TOKEN"),
		AccountID: os.Getenv("DATA_INTEGRATION_ACCOUNT_ID"),
	})
	if err != nil {
		return fmt.Errorf("checkEnvironmentDestroyed: build client: %w", err)
	}

	for name, rs := range s.RootModule().Resources {
		if rs.Type != "boomi_environment" {
			continue
		}
		id := rs.Primary.ID
		env, err := c.GetEnvironment(context.Background(), id)
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				continue // 404 — cleanly gone
			}
			return fmt.Errorf("checkEnvironmentDestroyed: GET %s (id=%s): %w", name, id, err)
		}
		if isTrue(env["is_deleted"]) || isTrue(env["is_delete_lock"]) {
			// is_deleted=true  → gone
			// is_delete_lock=true → async deletion queued (API returns 200 with status "W",
			// then the worker marks is_deleted=true asynchronously). The provider's Read
			// already treats is_delete_lock=true as removed. Accept it here too.
			continue
		}
		// Still alive with no deletion in progress — the destroy step silently failed.
		raw, _ := json.MarshalIndent(env, "", "  ")
		return fmt.Errorf(
			"checkEnvironmentDestroyed: %s (id=%s) still exists — is_deleted=false and is_delete_lock=false.\n"+
				"MongoDB document:\n%s", name, id, string(raw))
	}
	return nil
}

// TestAccSweepEnvironments deletes all tf-acc-* environments from the test
// account. Run before a test suite to clear orphaned state from previous
// failed or crashed runs — CheckDestroy only fires when tests complete normally.
// Together they cover both cleanup paths.
//
// Usage:
//
//	TF_ACC=1 \
//	  DATA_INTEGRATION_API_TOKEN=... \
//	  DATA_INTEGRATION_ACCOUNT_ID=... \
//	  DATA_INTEGRATION_API_URL=http://localhost:8008 \
//	  go test ./internal/provider/ -run TestAccSweepEnvironments -v
//
// The sweeper calls DELETE for each match and logs results; it ignores
// individual deletion errors (e.g. 409 "already deleting") and keeps going.
func TestAccSweepEnvironments(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set — set TF_ACC=1 to run sweepers")
	}
	c := testAccClientFromEnv(t)
	ctx := context.Background()

	envs, err := c.ListEnvironments(ctx)
	if err != nil {
		t.Fatalf("sweeper: listing environments: %v", err)
	}
	t.Logf("sweeper: %d total environment(s) in account", len(envs))

	swept := 0
	for _, env := range envs {
		name := asString(env["name"])
		if !strings.HasPrefix(name, "tf-acc-") {
			continue
		}
		id := asString(env["id"])
		raw, _ := json.MarshalIndent(env, "", "  ")
		t.Logf("sweeper: deleting %s (id=%s)\n  MongoDB document before delete:\n%s", name, id, raw)

		if err := c.DeleteEnvironment(ctx, id); err != nil {
			t.Logf("sweeper: DELETE %s (id=%s): %v (ignored — continuing)", name, id, err)
			continue
		}
		t.Logf("sweeper: ✓ deleted %s (id=%s)", name, id)
		swept++
	}
	t.Logf("sweeper: removed %d environment(s) with tf-acc- prefix", swept)
}

// envOrDefault returns the env var value or def when the var is unset or empty.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
