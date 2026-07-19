package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*connectionResource)(nil)
	_ resource.ResourceWithConfigure   = (*connectionResource)(nil)
	_ resource.ResourceWithImportState = (*connectionResource)(nil)
)

// secretAPIFields are field names that contain actual secret values returned by
// the API. Strip these from connection_info so state never holds credentials.
var secretAPIFields = map[string]bool{
	"password": true, "account_key": true, "access_token": true,
	"personal_access_token": true, "aws_access_secret": true,
	"aws_access_key": true, "service_account_json": true,
	"private_key": true, "secret_key": true, "api_key": true,
	"client_secret": true, "api_token": true, "credentials": true,
}

// NewConnectionResource is the factory registered with the provider.
func NewConnectionResource() resource.Resource { return &connectionResource{} }

type connectionResource struct {
	data *providerData
}

type connectionModel struct {
	ID              types.String         `tfsdk:"id"`
	EnvironmentID   types.String         `tfsdk:"environment_id"`
	Name            types.String         `tfsdk:"name"`
	Type            types.String         `tfsdk:"type"`
	ParametersJSON  jsontypes.Normalized `tfsdk:"parameters_json"`
	FzConnectionID  types.String         `tfsdk:"fz_connection_id"`
	ConnectionInfo  jsontypes.Normalized `tfsdk:"connection_info"`
	SshPkeyFile     types.String         `tfsdk:"ssh_pkey_file"`
	SshPkeyFilePath types.String         `tfsdk:"ssh_pkey_file_path"`
}

func (r *connectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection"
}

func (r *connectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Data Integration connection to a data source or target.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Connection ID, assigned by the API.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment this connection belongs to. Falls back to the " +
					"provider-level environment_id. Changing it forces a new connection.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Connection name.",
			},
			"type": schema.StringAttribute{
				Required: true,
				Description: "Connection type identifier (e.g. \"snowflake\", \"postgres\"). " +
					"Changing it forces a new connection.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"parameters_json": schema.StringAttribute{
				Optional:   true,
				Sensitive:  true,
				WriteOnly:  true,
				CustomType: jsontypes.NormalizedType{},
				Description: "Connection-type-specific parameters as a JSON object, including " +
					"credentials. Write-only: never stored in state. The API omits secrets on " +
					"read, so drift detection for credentials is not possible.",
			},
			"fz_connection_id": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Cross-ID of the file-zone staging connection linked to this connection.",
			},
			"connection_info": schema.StringAttribute{
				Computed:   true,
				CustomType: jsontypes.NormalizedType{},
				Description: "Non-sensitive fields returned by the API on every read " +
					"(e.g. username, warehouse, host, role). Populated automatically — " +
					"do not set manually. Secret fields are never included.",
			},
			"ssh_pkey_file": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "Local path to a PEM private-key file used for SSH tunnel authentication. " +
					"When set, the provider uploads the file on Create/Update via the connection-files API " +
					"and stores the resulting server-side path in ssh_pkey_file_path. Write-only: never " +
					"returned by the API, so this value is preserved from configuration and not refreshed.",
			},
			"ssh_pkey_file_path": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Server-side path of the uploaded SSH private-key file. " +
					"Populated automatically when ssh_pkey_file is provided; read back from the API on refresh. " +
					"Can also be set explicitly to carry over a path from an imported connection without re-uploading the key.",
			},
		},
	}
}

func (r *connectionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *connectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// WriteOnly attributes are erased from the plan by the framework (they are never stored
	// in state). Read parameters_json from the config so the credentials reach the API.
	if plan.ParametersJSON.IsNull() || plan.ParametersJSON.IsUnknown() {
		var cfg connectionModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.ParametersJSON = cfg.ParametersJSON
	}

	envID := resolveEnvironmentID(plan.EnvironmentID.ValueString(), r.data)
	if envID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("environment_id"), "Missing environment_id",
			"Set environment_id on the resource or environment_id on the provider.")
		return
	}

	// Upload SSH key before creating so its path lands in the same body as
	// credentials — a separate PATCH after creation would wipe secrets, because
	// the API redacts them on GET (read-modify-write would overwrite with empty).
	var sshFilePath string
	if !plan.SshPkeyFile.IsNull() && !plan.SshPkeyFile.IsUnknown() {
		fp, uploadErr := r.data.client.UploadConnectionFile(ctx, envID, plan.Type.ValueString(), plan.SshPkeyFile.ValueString())
		if uploadErr != nil {
			addAPIError(&resp.Diagnostics, "Error uploading SSH key file", uploadErr)
			return
		}
		sshFilePath = fp
	}

	// The connections API speaks connection_name / connection_type (not the
	// generic name/type used by rivers and environments).
	body := map[string]any{"connection_name": plan.Name.ValueString(), "connection_type": plan.Type.ValueString()}
	if !plan.FzConnectionID.IsNull() && plan.FzConnectionID.ValueString() != "" {
		body["fz_connection_id"] = plan.FzConnectionID.ValueString()
	}
	if sshFilePath != "" {
		body["ssh_pkey_file_path"] = sshFilePath
	} else if !plan.SshPkeyFilePath.IsNull() && !plan.SshPkeyFilePath.IsUnknown() {
		// Allow explicitly carrying over a server-side path (e.g. copied from an import).
		body["ssh_pkey_file_path"] = plan.SshPkeyFilePath.ValueString()
	}
	if params, ok := r.decodeParams(plan, &resp.Diagnostics); ok {
		mergeParams(body, params)
	} else if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.data.client.CreateConnection(ctx, envID, body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating connection", err)
		return
	}
	r.apply(created, envID, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state connectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn, err := r.data.client.GetConnection(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading connection", err)
		return
	}
	r.apply(conn, state.EnvironmentID.ValueString(), &state, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *connectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan connectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// WriteOnly attributes are erased from the plan by the framework. Read
	// parameters_json from the config so credentials are applied on updates too.
	if plan.ParametersJSON.IsNull() || plan.ParametersJSON.IsUnknown() {
		var cfg connectionModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.ParametersJSON = cfg.ParametersJSON
	}

	envID := plan.EnvironmentID.ValueString()

	params, hasParams := r.decodeParams(plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// Guard: the Rivery API omits secrets on GET, so a PUT without credentials
	// clears any existing password. If the caller isn't providing credentials
	// and the current connection has a password set, refuse the update to avoid
	// silent data loss. The caller must supply parameters_json to update a
	// connection that already has credentials.
	if !hasParams {
		current, err := r.data.client.GetConnection(ctx, envID, plan.ID.ValueString())
		if err != nil {
			addAPIError(&resp.Diagnostics, "Error reading connection before update", err)
			return
		}
		if current["password_exists"] == true ||
			current["account_key_exists"] == true ||
			current["api_token_exists"] == true {
			resp.Diagnostics.AddAttributeError(
				path.Root("parameters_json"),
				"parameters_json required for update",
				"This connection has credentials set in the API. Updating without "+
					"parameters_json would clear them. Provide the current credentials "+
					"in parameters_json to proceed.",
			)
			return
		}
	}

	// Upload SSH key before the PUT so its path is included in the same write
	// as the credentials — avoids a second read-modify-write that would wipe secrets.
	var sshFilePath string
	if !plan.SshPkeyFile.IsNull() && !plan.SshPkeyFile.IsUnknown() {
		fp, uploadErr := r.data.client.UploadConnectionFile(ctx, envID, plan.Type.ValueString(), plan.SshPkeyFile.ValueString())
		if uploadErr != nil {
			addAPIError(&resp.Diagnostics, "Error uploading SSH key file", uploadErr)
			return
		}
		sshFilePath = fp
	}

	patch := map[string]any{"connection_name": plan.Name.ValueString(), "connection_type": plan.Type.ValueString()}
	if !plan.FzConnectionID.IsNull() && plan.FzConnectionID.ValueString() != "" {
		patch["fz_connection_id"] = plan.FzConnectionID.ValueString()
	}
	if sshFilePath != "" {
		patch["ssh_pkey_file_path"] = sshFilePath
	} else if !plan.SshPkeyFilePath.IsNull() && !plan.SshPkeyFilePath.IsUnknown() {
		// Preserve the existing server-side path when not re-uploading (e.g. after import).
		patch["ssh_pkey_file_path"] = plan.SshPkeyFilePath.ValueString()
	}
	if hasParams {
		mergeParams(patch, params)
	}

	updated, err := r.data.client.UpdateConnection(ctx, envID, plan.ID.ValueString(), patch)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating connection", err)
		return
	}
	r.apply(updated, envID, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *connectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state connectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.DeleteConnection(ctx, state.EnvironmentID.ValueString(), state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		addAPIError(&resp.Diagnostics, "Error deleting connection", err)
	}
}

func (r *connectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	envID, id, err := splitImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	if envID == "" {
		envID = resolveEnvironmentID("", r.data)
	}
	if envID == "" {
		resp.Diagnostics.AddError("Missing environment_id for import",
			"Use \"<environment_id>/<connection_id>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}

// apply maps an API response onto the model.
// parameters_json is intentionally left untouched (write-only secret handling).
// connection_info is populated from the API response with secret fields stripped.
func (r *connectionResource) apply(api map[string]any, envID string, m *connectionModel, diags *diag.Diagnostics) {
	m.ID = types.StringValue(asString(api["id"]))
	m.EnvironmentID = types.StringValue(envID)
	m.Name = types.StringValue(asString(api["name"]))
	// API returns "connection_type", not "type".
	if t := asString(api["connection_type"]); t != "" {
		m.Type = types.StringValue(t)
	}
	if fz := asString(api["fz_connection_id"]); fz != "" {
		m.FzConnectionID = types.StringValue(fz)
	} else {
		m.FzConnectionID = types.StringNull()
	}
	if fp := asString(api["ssh_pkey_file_path"]); fp != "" {
		m.SshPkeyFilePath = types.StringValue(fp)
	} else {
		m.SshPkeyFilePath = types.StringNull()
	}
	// SshPkeyFile is intentionally not updated here — it is write-only and never
	// returned by the API, so it must be preserved from prior plan/state.

	// Build connection_info: full API response minus any secret fields.
	safe := make(map[string]any, len(api))
	for k, v := range api {
		if !secretAPIFields[k] {
			safe[k] = v
		}
	}
	infoBytes, err := json.Marshal(safe)
	if err != nil {
		diags.AddWarning("connection_info serialization failed", err.Error())
		m.ConnectionInfo = jsontypes.NewNormalizedNull()
	} else {
		m.ConnectionInfo = jsontypes.NewNormalizedValue(string(infoBytes))
	}
}

// decodeParams parses parameters_json into a map. Returns (nil,false) when
// unset; appends a diagnostic and returns (nil,false) on malformed JSON.
func (r *connectionResource) decodeParams(m connectionModel, diags *diag.Diagnostics) (map[string]any, bool) {
	if m.ParametersJSON.IsNull() || m.ParametersJSON.IsUnknown() {
		return nil, false
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(m.ParametersJSON.ValueString()), &params); err != nil {
		diags.AddAttributeError(path.Root("parameters_json"), "Invalid parameters_json",
			fmt.Sprintf("parameters_json must be a JSON object: %s", err))
		return nil, false
	}
	return params, true
}

// mergeParams copies type-specific params into the request body without
// clobbering the reserved top-level keys.
func mergeParams(body, params map[string]any) {
	for k, v := range params {
		if k == "name" || k == "type" || k == "ssh_pkey_file_path" {
			continue
		}
		body[k] = v
	}
}
