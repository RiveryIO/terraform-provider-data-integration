package provider

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// configureProviderData unwraps the providerData passed via Configure. It
// tolerates a nil ProviderData (Configure is called twice — once before the
// provider itself is configured) and reports a clear error on a type mismatch.
func configureProviderData(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *providerData {
	if req.ProviderData == nil {
		return nil
	}
	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *providerData, got %T. This is a provider bug.", req.ProviderData),
		)
		return nil
	}
	return data
}

// resolveEnvironmentID picks the resource-level environment_id when set,
// otherwise falls back to the provider-level default. Returns "" when neither
// is available (the caller raises an attribute error).
func resolveEnvironmentID(resourceEnvID string, data *providerData) string {
	if resourceEnvID != "" {
		return resourceEnvID
	}
	if data != nil {
		return data.defaultEnvironmentID
	}
	return ""
}

// splitImportID parses an env-scoped import identifier of the form
// "<environment_id>/<resource_id>". A bare id is allowed when a provider-level
// default environment_id is configured.
func splitImportID(raw string) (envID, id string, err error) {
	parts := strings.SplitN(raw, "/", 2)
	if len(parts) == 2 {
		if parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("expected \"<environment_id>/<id>\", got %q", raw)
		}
		return parts[0], parts[1], nil
	}
	// bare id — environment_id must come from the provider default
	return "", raw, nil
}

// asString coerces an arbitrary JSON-decoded value to its string form. Numbers
// are rendered without trailing ".0" where they are integral, so an id that
// arrives as a JSON number still round-trips as a stable string.
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
