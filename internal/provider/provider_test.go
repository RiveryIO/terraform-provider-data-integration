package provider

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
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
	for _, want := range []string{"api_url", "token", "account_id", "environment_id"} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("provider schema missing attribute %q", want)
		}
	}
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
