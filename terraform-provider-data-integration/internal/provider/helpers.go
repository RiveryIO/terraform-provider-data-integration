package provider

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
)

// addAPIError appends a diagnostic for a failed Data Integration API call.
// Authentication and authorization failures get an actionable message so a
// practitioner can resolve them without reading the raw HTTP error — the most
// common real-world failure modes for this provider. All other errors fall
// through to the underlying client message (which already carries status +
// server detail).
func addAPIError(diags *diag.Diagnostics, action string, err error) {
	var apiErr *client.APIError
	detail := ""
	if errors.As(err, &apiErr) {
		detail = apiErr.Details
	}

	switch {
	case errors.Is(err, client.ErrUnauthorized):
		diags.AddError(action+": authentication failed (401)",
			"The Data Integration API rejected the credentials.\n\n"+
				"Fix: verify the provider `token` is set, valid, and not expired, and that "+
				"`api_url` targets the correct region (e.g. https://api.integration.rivery.in "+
				"for integration). The token and `account_id` must belong to the same account."+
				detailSuffix(detail))
	case errors.Is(err, client.ErrForbidden):
		diags.AddError(action+": insufficient permissions (403)",
			"The credentials authenticated but are not authorized for this operation.\n\n"+
				"Fix: Data Integration token scopes are granted per-environment. Confirm the "+
				"token has the required role (admin/write) on the target `account_id` and "+
				"`environment_id`. Note a newly created environment is NOT covered by a token "+
				"minted before it existed — use an account-admin token or re-issue the token."+
				detailSuffix(detail))
	default:
		diags.AddError(action, err.Error())
	}
}

func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return "\n\nAPI detail: " + detail
}

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

// isTrue reports whether a JSON-decoded value represents boolean true, tolerating
// the bool, "true" string, and numeric 1 encodings the API may use.
func isTrue(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(t, "true")
	case float64:
		return t != 0
	default:
		return false
	}
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
