package provider

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
		"variable":    NewVariableResource,
		"dataframe":   NewDataFrameResource,
		"cdc_config":  NewCDCConfigResource,
		"team":        NewTeamResource,
		"key_pair":    NewKeyPairResource,
		"recipe":      NewRecipeResource,
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

func TestMergeParams(t *testing.T) {
	cases := []struct {
		name   string
		body   map[string]any
		params map[string]any
		want   map[string]any
	}{
		{
			name:   "merges non-reserved keys",
			body:   map[string]any{"connection_name": "test"},
			params: map[string]any{"bucket": "my-bucket", "region": "us-east-1"},
			want:   map[string]any{"connection_name": "test", "bucket": "my-bucket", "region": "us-east-1"},
		},
		{
			name:   "skips reserved key name",
			body:   map[string]any{"connection_name": "test"},
			params: map[string]any{"name": "should-be-skipped", "bucket": "my-bucket"},
			want:   map[string]any{"connection_name": "test", "bucket": "my-bucket"},
		},
		{
			name:   "skips reserved key type",
			body:   map[string]any{"connection_type": "s3"},
			params: map[string]any{"type": "should-be-skipped", "key": "val"},
			want:   map[string]any{"connection_type": "s3", "key": "val"},
		},
		{
			name:   "nil params is a no-op",
			body:   map[string]any{"connection_name": "test"},
			params: nil,
			want:   map[string]any{"connection_name": "test"},
		},
		{
			name:   "empty params is a no-op",
			body:   map[string]any{"connection_name": "test"},
			params: map[string]any{},
			want:   map[string]any{"connection_name": "test"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mergeParams(tc.body, tc.params)
			for k, want := range tc.want {
				if got, ok := tc.body[k]; !ok || got != want {
					t.Errorf("body[%q] = %v (ok=%v), want %v", k, got, ok, want)
				}
			}
			// Confirm reserved keys were not injected into body.
			for _, reserved := range []string{"name", "type"} {
				if _, inParams := tc.params[reserved]; inParams {
					if _, inBody := tc.body[reserved]; inBody {
						t.Errorf("reserved key %q must not be copied into body", reserved)
					}
				}
			}
		})
	}
}

func TestIsTrue(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true},
		{false, false},
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"false", false},
		{"FALSE", false},
		{float64(1), true},
		{float64(0), false},
		{float64(42), true},
		{nil, false},
		{"yes", false},
		{"1", false}, // only the string "true" is truthy, not "1"
	}
	for _, tc := range cases {
		if got := isTrue(tc.in); got != tc.want {
			t.Errorf("isTrue(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestConnSettingsBody(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		if got := connSettingsBody(nil); got != nil {
			t.Errorf("connSettingsBody(nil) = %v, want nil", got)
		}
	})
	t.Run("all fields are mapped", func(t *testing.T) {
		cs := &dataFrameConnSettings{
			Connection:    types.StringValue("conn-id"),
			DatasourceID:  types.StringValue("aws"),
			StorageType:   types.StringValue("s3"),
			DefaultBucket: types.StringValue("my-bucket"),
		}
		got := connSettingsBody(cs)
		expect := map[string]string{
			"connection":     "conn-id",
			"datasource_id":  "aws",
			"storage_type":   "s3",
			"default_bucket": "my-bucket",
		}
		for k, want := range expect {
			if v, ok := got[k]; !ok || v != want {
				t.Errorf("connSettingsBody[%q] = %v (ok=%v), want %q", k, v, ok, want)
			}
		}
	})
}
