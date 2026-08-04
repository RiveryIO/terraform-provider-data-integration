package provider

import (
	"context"
	"errors"

	"github.com/boomi/terraform-provider-data-integration/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*dataFrameResource)(nil)
	_ resource.ResourceWithConfigure   = (*dataFrameResource)(nil)
	_ resource.ResourceWithImportState = (*dataFrameResource)(nil)
)

// NewDataFrameResource is the factory registered with the provider.
func NewDataFrameResource() resource.Resource { return &dataFrameResource{} }

type dataFrameResource struct {
	data *providerData
}

type dataFrameModel struct {
	ID                 types.String           `tfsdk:"id"`
	EnvironmentID      types.String           `tfsdk:"environment_id"`
	Name               types.String           `tfsdk:"name"`
	ConnectionSettings *dataFrameConnSettings `tfsdk:"connection_settings"`
}

type dataFrameConnSettings struct {
	Connection    types.String `tfsdk:"connection"`
	DatasourceID  types.String `tfsdk:"datasource_id"`
	StorageType   types.String `tfsdk:"storage_type"`
	DefaultBucket types.String `tfsdk:"default_bucket"`
}

func (r *dataFrameResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dataframe"
}

func (r *dataFrameResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A Data Integration dataframe — a named parquet store used by logicode (Python) data flows.\n\n" +
			"Two storage types exist:\n" +
			"  • **Internal** (data-flow-managed): omit `connection_settings`. " +
			"The platform provisions and credentials the underlying storage for you. " +
			"The only creation requirement is `name`.\n" +
			"  • **File-zone** (custom): include `connection_settings` pointing to a " +
			"`boomi_data_integration_connection` that owns your own S3/GCS/Azure Blob bucket. " +
			"The connection_settings block is the only field updatable in place.\n\n" +
			"The API keys dataframes by `name` (no separate cross_id is returned). " +
			"Changing the name forces a new dataframe — the parquet files written under the old " +
			"name are orphaned, not migrated.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Resource id — equals the dataframe name (the API uses name as the identifier).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"environment_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Description: "Environment this dataframe belongs to. Falls back to the " +
					"provider-level environment_id. Changing it forces a new dataframe.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				Description: "Dataframe name — must be unique within the environment and must match the " +
					"import name used in the data flow's Python code (`from rivery_dataframes import <name>`). " +
					"The API does not support renaming, so changing it forces a new dataframe.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"connection_settings": schema.SingleNestedAttribute{
				Optional: true,
				Computed: true,
				Description: "Storage connection for a file-zone (custom) dataframe. " +
					"Omit this block to create an internal (data-flow-managed) dataframe whose storage and " +
					"credentials the platform manages automatically. " +
					"When present, this block is the only field the API allows to be updated in place.",
				Attributes: map[string]schema.Attribute{
					"connection": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "ID of the storage connection (cross-reference a boomi_data_integration_connection).",
					},
					"datasource_id": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Datasource identifier of the connection (e.g. \"s3\", \"gcs\").",
					},
					"storage_type": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Storage type (e.g. \"s3\", \"aws\", \"gcs\").",
					},
					"default_bucket": schema.StringAttribute{
						Optional:    true,
						Computed:    true,
						Description: "Default bucket the dataframe writes its parquet files to.",
					},
				},
			},
		},
	}
}

func (r *dataFrameResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req, resp)
}

func (r *dataFrameResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dataFrameModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	envID := resolveEnvironmentID(plan.EnvironmentID.ValueString(), r.data)
	if envID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("environment_id"), "Missing environment_id",
			"Set environment_id on the resource or environment_id on the provider.")
		return
	}

	body := map[string]any{"name": plan.Name.ValueString()}
	if cs := connSettingsBody(plan.ConnectionSettings); cs != nil {
		body["connection_settings"] = cs
	}

	created, err := r.data.client.CreateDataFrame(ctx, envID, body)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error creating dataframe", err)
		return
	}
	r.apply(created, envID, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dataFrameResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dataFrameModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	df, err := r.data.client.GetDataFrame(ctx, state.EnvironmentID.ValueString(), state.Name.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx) // drift: deleted out-of-band
			return
		}
		addAPIError(&resp.Diagnostics, "Error reading dataframe", err)
		return
	}
	// connection_settings is config-authoritative (kept from prior state, not
	// refreshed) so an apply plans clean; only identity fields are mapped back.
	r.apply(df, state.EnvironmentID.ValueString(), &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *dataFrameResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dataFrameModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	envID := plan.EnvironmentID.ValueString()

	// The API only accepts connection_settings on update.
	patch := map[string]any{}
	if cs := connSettingsBody(plan.ConnectionSettings); cs != nil {
		patch["connection_settings"] = cs
	}

	updated, err := r.data.client.UpdateDataFrame(ctx, envID, plan.Name.ValueString(), patch)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Error updating dataframe", err)
		return
	}
	r.apply(updated, envID, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dataFrameResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dataFrameModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.data.client.DeleteDataFrame(ctx, state.EnvironmentID.ValueString(), state.Name.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return // already gone
		}
		addAPIError(&resp.Diagnostics, "Error deleting dataframe", err)
	}
}

// ImportState accepts "<environment_id>/<name>" (or a bare name when a
// provider-level environment_id is set).
func (r *dataFrameResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	envID, name, err := splitImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	if envID == "" {
		envID = resolveEnvironmentID("", r.data)
	}
	if envID == "" {
		resp.Diagnostics.AddError("Missing environment_id for import",
			"Use \"<environment_id>/<name>\" or set environment_id on the provider.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment_id"), envID)...)
}

// apply maps fields from an API response onto the model. The dataframe is keyed
// by name so id mirrors name. connection_settings is mapped back from the API
// when present (file-zone dataframes), enabling post-import state population and
// drift detection. Internal dataframes have no connection_settings in the
// API response — the block stays nil in that case.
func (r *dataFrameResource) apply(api map[string]any, envID string, m *dataFrameModel) {
	name := asString(api["name"])
	if name == "" {
		name = m.Name.ValueString()
	}
	m.ID = types.StringValue(name)
	m.Name = types.StringValue(name)
	m.EnvironmentID = types.StringValue(envID)

	// Read back connection_settings for file-zone dataframes. Internal
	// dataframes return no connection_settings — leave the block nil.
	if raw, ok := api["connection_settings"]; ok && raw != nil {
		if cs, ok := raw.(map[string]any); ok && len(cs) > 0 {
			m.ConnectionSettings = &dataFrameConnSettings{
				Connection:    types.StringValue(asString(cs["connection"])),
				DatasourceID:  types.StringValue(asString(cs["datasource_id"])),
				StorageType:   types.StringValue(asString(cs["storage_type"])),
				DefaultBucket: types.StringValue(asString(cs["default_bucket"])),
			}
		}
	}
}

// connSettingsBody renders the typed nested block into the API's
// connection_settings object, or nil when unset.
func connSettingsBody(cs *dataFrameConnSettings) map[string]any {
	if cs == nil {
		return nil
	}
	return map[string]any{
		"connection":     cs.Connection.ValueString(),
		"datasource_id":  cs.DatasourceID.ValueString(),
		"storage_type":   cs.StorageType.ValueString(),
		"default_bucket": cs.DefaultBucket.ValueString(),
	}
}
