package provider

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// testAccProtoV6ProviderFactories wires the in-process provider for acceptance
// tests (used only when TF_ACC is set).
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"boomi": providerserver.NewProtocol6WithError(New("test")()),
}

func TestProviderSchemaValid(t *testing.T) {
	p := New("test")()
	resp := &fwprovider.SchemaResponse{}
	p.Schema(context.Background(), fwprovider.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("provider schema diagnostics: %v", resp.Diagnostics)
	}
	for _, want := range []string{
		"api_url", "token", "account_id", "environment_id",
		"boomi_platform_url", "boomi_account_id", "boomi_username", "boomi_api_token",
	} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("provider schema missing attribute %q", want)
		}
	}
}

// strVal builds the tftypes.Value leaves newProviderConfig needs — every
// provider attribute today is a plain string. Attributes omitted from a
// test's values map get their null leaf inline, in newProviderConfig itself.
func strVal(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }

// newProviderConfig builds a tfsdk.Config for Configure tests from the
// provider's own schema, so it stays in sync as attributes are added.
// Attributes omitted from values are sent as null, matching an unset HCL
// argument.
func newProviderConfig(t *testing.T, p fwprovider.Provider, values map[string]tftypes.Value) tfsdk.Config {
	t.Helper()
	schemaResp := &fwprovider.SchemaResponse{}
	p.Schema(context.Background(), fwprovider.SchemaRequest{}, schemaResp)

	objType := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	full := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		if v, ok := values[name]; ok {
			full[name] = v
		} else {
			full[name] = tftypes.NewValue(typ, nil)
		}
	}
	return tfsdk.Config{Raw: tftypes.NewValue(objType, full), Schema: schemaResp.Schema}
}

func TestConfigure_StaticToken(t *testing.T) {
	p := New("test")()
	req := fwprovider.ConfigureRequest{Config: newProviderConfig(t, p, map[string]tftypes.Value{
		"api_url":    strVal("http://example.test"),
		"token":      strVal("tok"),
		"account_id": strVal("acct"),
	})}
	resp := &fwprovider.ConfigureResponse{}
	p.Configure(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
}

func TestConfigure_BoomiJWT(t *testing.T) {
	p := New("test")()
	req := fwprovider.ConfigureRequest{Config: newProviderConfig(t, p, map[string]tftypes.Value{
		"api_url":          strVal("http://example.test"),
		"account_id":       strVal("acct"),
		"boomi_account_id": strVal("boomi-acct"),
		"boomi_username":   strVal("user@example.com"),
		"boomi_api_token":  strVal("boomi-tok"),
	})}
	resp := &fwprovider.ConfigureResponse{}
	p.Configure(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
}

func TestConfigure_AuthValidation(t *testing.T) {
	cases := []struct {
		name    string
		values  map[string]tftypes.Value
		wantErr string
	}{
		{
			name:    "no auth configured",
			values:  map[string]tftypes.Value{"account_id": strVal("acct")},
			wantErr: "Missing authentication",
		},
		{
			name: "token and boomi both set",
			values: map[string]tftypes.Value{
				"account_id":       strVal("acct"),
				"token":            strVal("tok"),
				"boomi_account_id": strVal("boomi-acct"),
			},
			wantErr: "Conflicting authentication configuration",
		},
		{
			name: "partial boomi set (missing api_token)",
			values: map[string]tftypes.Value{
				"account_id":       strVal("acct"),
				"boomi_account_id": strVal("boomi-acct"),
				"boomi_username":   strVal("user@example.com"),
			},
			wantErr: "Missing Boomi API token",
		},
		{
			name: "missing account_id regardless of auth mode",
			values: map[string]tftypes.Value{
				"token": strVal("tok"),
			},
			wantErr: "Missing account ID",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New("test")()
			req := fwprovider.ConfigureRequest{Config: newProviderConfig(t, p, tc.values)}
			resp := &fwprovider.ConfigureResponse{}
			p.Configure(context.Background(), req, resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected diagnostics containing %q, got none", tc.wantErr)
			}
			var found bool
			for _, d := range resp.Diagnostics {
				if contains(d.Summary(), tc.wantErr) {
					found = true
				}
			}
			if !found {
				t.Errorf("diagnostics = %v, want one containing %q", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestResourceSchemasValid exercises each resource's Schema so a malformed
// schema (bad default, duplicate attr, etc.) fails fast in unit tests rather
// than at terraform plan time.
func TestResourceSchemasValid(t *testing.T) {
	factories := map[string]func() resource.Resource{
		"environment": NewEnvironmentResource,
		"connection":  NewConnectionResource,
		"data_flow":   NewDataFlowResource,
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			resp := &resource.SchemaResponse{}
			factory().Schema(context.Background(), resource.SchemaRequest{}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("%s schema diagnostics: %v", name, resp.Diagnostics)
			}
			if _, ok := resp.Schema.Attributes["id"]; !ok {
				t.Errorf("%s schema missing computed id attribute", name)
			}
		})
	}
}

func TestSplitImportID(t *testing.T) {
	cases := []struct {
		in      string
		wantEnv string
		wantID  string
		wantErr bool
	}{
		{"env1/res1", "env1", "res1", false},
		{"bareid", "", "bareid", false},
		{"env1/", "", "", true},
		{"/res1", "", "", true},
	}
	for _, tc := range cases {
		env, id, err := splitImportID(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("splitImportID(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if err == nil && (env != tc.wantEnv || id != tc.wantID) {
			t.Errorf("splitImportID(%q) = (%q,%q), want (%q,%q)", tc.in, env, id, tc.wantEnv, tc.wantID)
		}
	}
}

func TestResolveEnvironmentID(t *testing.T) {
	data := &providerData{defaultEnvironmentID: "default-env"}
	if got := resolveEnvironmentID("res-env", data); got != "res-env" {
		t.Errorf("resource value should win, got %q", got)
	}
	if got := resolveEnvironmentID("", data); got != "default-env" {
		t.Errorf("should fall back to provider default, got %q", got)
	}
	if got := resolveEnvironmentID("", nil); got != "" {
		t.Errorf("nil data should yield empty, got %q", got)
	}
}

func TestAsString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"abc", "abc"},
		{float64(42), "42"}, // integral number → no trailing .0
		{float64(1.5), "1.5"},
		{true, "true"},
	}
	for _, tc := range cases {
		if got := asString(tc.in); got != tc.want {
			t.Errorf("asString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
