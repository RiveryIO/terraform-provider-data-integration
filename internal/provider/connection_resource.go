package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	FileParams      types.Map            `tfsdk:"file_params"`
	FileParamPaths  types.Map            `tfsdk:"file_param_paths"`
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
			"file_params": schema.MapAttribute{
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
				Description: "Map of connection-body field name → local file path. For each entry the " +
					"provider uploads the file via the connection-files API and injects the returned " +
					"server-side path into the connection body under that field name. " +
					"Use this for any credential file: Snowflake P8 keys, GCS/BQ service-account JSON, " +
					"SSH keys, etc. The uploaded paths are stored in file_param_paths. " +
					"Write-only: local paths are never stored in state.",
			},
			"file_param_paths": schema.MapAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "Server-side paths returned after uploading the files in file_params. " +
					"Keys match the field names from file_params. Populated automatically on " +
					"Create/Update; read back from the API on refresh. Can be set explicitly to " +
					"carry over paths from an imported connection without re-uploading.",
			},
			"ssh_pkey_file": schema.StringAttribute{
				Optional:           true,
				Sensitive:          true,
				DeprecationMessage: "Use file_params = { ssh_pkey_file_path = \"<local path>\" } instead.",
				Description: "Deprecated: use file_params. Local path to a PEM private-key file for " +
					"SSH tunnel authentication. Uploads via the connection-files API and stores the " +
					"server-side path in ssh_pkey_file_path.",
			},
			"ssh_pkey_file_path": schema.StringAttribute{
				Optional:           true,
				Computed:           true,
				DeprecationMessage: "Use file_param_paths instead.",
				Description: "Deprecated: use file_param_paths. Server-side path of the uploaded SSH " +
					"private-key file. Populated automatically when ssh_pkey_file is provided.",
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
	// in state). Read both write-only fields from the config so credentials reach the API.
	var cfg connectionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.ParametersJSON.IsNull() || plan.ParametersJSON.IsUnknown() {
		plan.ParametersJSON = cfg.ParametersJSON
	}
	if plan.FileParams.IsNull() || plan.FileParams.IsUnknown() {
		plan.FileParams = cfg.FileParams
	}

	envID := resolveEnvironmentID(plan.EnvironmentID.ValueString(), r.data)
	if envID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("environment_id"), "Missing environment_id",
			"Set environment_id on the resource or environment_id on the provider.")
		return
	}

	// Upload all credential files before creating — paths must land in the same
	// body as parameters_json so secrets aren't wiped by a separate PATCH.
	filePaths := uploadFileParams(ctx, r.data.client, envID, plan.Type.ValueString(), plan.FileParams, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// Legacy ssh_pkey_file support (deprecated — prefer file_params).
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
	// generic name/type used by data flows and environments).
	body := map[string]any{"connection_name": plan.Name.ValueString(), "connection_type": plan.Type.ValueString()}
	if !plan.FzConnectionID.IsNull() && plan.FzConnectionID.ValueString() != "" {
		body["fz_connection_id"] = plan.FzConnectionID.ValueString()
	}
	for field, serverPath := range filePaths {
		body[field] = serverPath
	}
	if sshFilePath != "" {
		body["ssh_pkey_file_path"] = sshFilePath
	} else if !plan.SshPkeyFilePath.IsNull() && !plan.SshPkeyFilePath.IsUnknown() {
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
	primeFileParamPaths(&plan, filePaths, &resp.Diagnostics)
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
	// both write-only fields from the config so credentials are applied on updates too.
	var cfg connectionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.ParametersJSON.IsNull() || plan.ParametersJSON.IsUnknown() {
		plan.ParametersJSON = cfg.ParametersJSON
	}
	if plan.FileParams.IsNull() || plan.FileParams.IsUnknown() {
		plan.FileParams = cfg.FileParams
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

	// Upload all credential files before the PUT — same atomicity requirement as Create.
	filePaths := uploadFileParams(ctx, r.data.client, envID, plan.Type.ValueString(), plan.FileParams, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// Legacy ssh_pkey_file support (deprecated — prefer file_params).
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
	for field, serverPath := range filePaths {
		patch[field] = serverPath
	}
	if sshFilePath != "" {
		patch["ssh_pkey_file_path"] = sshFilePath
	} else if !plan.SshPkeyFilePath.IsNull() && !plan.SshPkeyFilePath.IsUnknown() {
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
	primeFileParamPaths(&plan, filePaths, &resp.Diagnostics)
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

	// Rebuild file_param_paths from the API response: any field that was uploaded
	// via file_params will be present as a top-level key in the API response.
	// We read them back so state tracks the current server-side paths.
	if !m.FileParamPaths.IsNull() && !m.FileParamPaths.IsUnknown() {
		existing := m.FileParamPaths.Elements()
		refreshed := make(map[string]types.String, len(existing))
		for field := range existing {
			if v := asString(api[field]); v != "" {
				refreshed[field] = types.StringValue(v)
			}
		}
		rebuilt, mapDiags := types.MapValueFrom(context.Background(), types.StringType, refreshed)
		diags.Append(mapDiags...)
		m.FileParamPaths = rebuilt
	} else {
		m.FileParamPaths = types.MapValueMust(types.StringType, map[string]attr.Value{})
	}
	// FileParams is write-only (local paths) — never updated from API response.

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
// clobbering reserved keys or keys already set by file_params uploads.
func mergeParams(body, params map[string]any) {
	for k, v := range params {
		if k == "name" || k == "type" || k == "ssh_pkey_file_path" {
			continue
		}
		if _, alreadySet := body[k]; alreadySet {
			continue // file_params upload result takes priority
		}
		body[k] = v
	}
}

// primeFileParamPaths seeds FileParamPaths on the model from a fresh upload result.
// apply() will later refresh from the API response; this ensures the computed attribute
// is never unknown after Create/Update even when the API echoes nothing back.
func primeFileParamPaths(m *connectionModel, uploaded map[string]string, diags *diag.Diagnostics) {
	if len(uploaded) == 0 {
		if m.FileParamPaths.IsNull() || m.FileParamPaths.IsUnknown() {
			m.FileParamPaths = types.MapValueMust(types.StringType, map[string]attr.Value{})
		}
		return
	}
	vals := make(map[string]attr.Value, len(uploaded))
	for k, v := range uploaded {
		vals[k] = types.StringValue(v)
	}
	rebuilt, mapDiags := types.MapValue(types.StringType, vals)
	diags.Append(mapDiags...)
	if !diags.HasError() {
		m.FileParamPaths = rebuilt
	}
}

// uploadFileParams uploads each local file in the file_params map and returns
// a map of field_name → server_side_path to be merged into the connection body.
func uploadFileParams(
	ctx context.Context,
	client interface {
		UploadConnectionFile(context.Context, string, string, string) (string, error)
	},
	envID, connType string,
	fileParams types.Map,
	diags *diag.Diagnostics,
) map[string]string {
	if fileParams.IsNull() || fileParams.IsUnknown() {
		return nil
	}
	paths := make(map[string]string, len(fileParams.Elements()))
	for field, val := range fileParams.Elements() {
		localPath, ok := val.(types.String)
		if !ok || localPath.IsNull() || localPath.IsUnknown() {
			continue
		}
		serverPath, err := client.UploadConnectionFile(ctx, envID, connType, localPath.ValueString())
		if err != nil {
			diags.AddAttributeError(
				path.Root("file_params"),
				"Error uploading file for "+field,
				err.Error(),
			)
			return nil
		}
		paths[field] = serverPath
	}
	return paths
}
