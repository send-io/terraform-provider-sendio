package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/send-io/terraform-provider-sendio/internal/client"
)

var (
	_ resource.Resource                     = &apiKeyResource{}
	_ resource.ResourceWithImportState      = &apiKeyResource{}
	_ resource.ResourceWithConfigValidators = &apiKeyResource{}
)

func NewAPIKeyResource() resource.Resource { return &apiKeyResource{} }

type apiKeyResource struct{ resourceBase }

type apiKeyModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Permission  types.String `tfsdk:"permission"`
	DomainID    types.String `tfsdk:"domain_id"`
	Token       types.String `tfsdk:"token"`
	TokenPrefix types.String `tfsdk:"token_prefix"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (r *apiKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (r *apiKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An API key. The token is returned once, when the key is created, and is kept " +
			"(sensitive) in state; an imported key has no token. Destroying the resource revokes the key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "API key id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Key name (at most 50 characters). Renaming updates the key in place.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 50)},
			},
			"permission": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("full_access"),
				MarkdownDescription: "`full_access` (default) or `sending_access`. Changing it creates a new key.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.OneOf("full_access", "sending_access")},
			},
			"domain_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Restrict a `sending_access` key to one domain. Changing it creates a new key.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(uuidRE, "must be a domain id (UUID)")},
			},
			"token": schema.StringAttribute{
				Computed: true, Sensitive: true,
				MarkdownDescription: "The secret key. Only known after create; null for imported keys.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"token_prefix": schema.StringAttribute{
				Computed: true, MarkdownDescription: "First characters of the token, as shown in the dashboard.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "When the key was created.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *apiKeyResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{domainNeedsSendingAccess{}}
}

// domainNeedsSendingAccess: domain_id only narrows a sending_access key.
type domainNeedsSendingAccess struct{}

func (domainNeedsSendingAccess) Description(context.Context) string {
	return "domain_id requires permission = \"sending_access\""
}
func (v domainNeedsSendingAccess) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (domainNeedsSendingAccess) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var domainID, perm types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("domain_id"), &domainID)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("permission"), &perm)...)
	if domainID.IsNull() || domainID.IsUnknown() || perm.IsUnknown() {
		return
	}
	if perm.ValueString() != "sending_access" {
		resp.Diagnostics.AddAttributeError(path.Root("domain_id"), "domain_id needs sending_access",
			"Only a sending_access key can be limited to one domain. Set permission = \"sending_access\".")
	}
}

func (r *apiKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"name": plan.Name.ValueString(), "permission": plan.Permission.ValueString()}
	if !plan.DomainID.IsNull() {
		body["domain_id"] = plan.DomainID.ValueString()
	}
	var created client.Created
	if err := r.client.Post(ctx, "/api-keys", body, &created); err != nil {
		apiErr(&resp.Diagnostics, "create API key", err)
		return
	}
	plan.ID = types.StringValue(created.ID)
	plan.Token = types.StringValue(created.Token)
	// Keep the token even if the read below fails.
	early := plan
	early.TokenPrefix, early.CreatedAt = types.StringNull(), types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &early)...)
	var k client.APIKey
	if err := r.client.Get(ctx, client.Path("/api-keys", created.ID), &k); err != nil {
		apiErr(&resp.Diagnostics, "read API key "+created.ID, err)
		return
	}
	apiKeyToModel(&k, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func apiKeyToModel(k *client.APIKey, m *apiKeyModel) {
	m.ID = types.StringValue(k.ID)
	m.Name = types.StringValue(k.Name)
	m.Permission = types.StringValue(k.Permission)
	m.DomainID = strValue(k.DomainID)
	m.TokenPrefix = types.StringValue(k.TokenPrefix)
	m.CreatedAt = types.StringValue(k.CreatedAt)
	if m.Token.IsUnknown() {
		m.Token = types.StringNull()
	}
}

func (r *apiKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var k client.APIKey
	if err := r.client.Get(ctx, client.Path("/api-keys", state.ID.ValueString()), &k); err != nil {
		if client.IsNotFound(err) { // revoked keys read as not found
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read API key "+state.ID.ValueString(), err)
		return
	}
	apiKeyToModel(&k, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update can only rename (PATCH /api-keys/{id}); other changes replace.
func (r *apiKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if !plan.Name.Equal(state.Name) {
		if err := r.client.Patch(ctx, client.Path("/api-keys", id), map[string]any{"name": plan.Name.ValueString()}, nil); err != nil {
			apiErr(&resp.Diagnostics, "rename API key "+id, err)
			return
		}
	}
	var k client.APIKey
	if err := r.client.Get(ctx, client.Path("/api-keys", id), &k); err != nil {
		apiErr(&resp.Diagnostics, "read API key "+id, err)
		return
	}
	plan.Token = state.Token
	apiKeyToModel(&k, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *apiKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, client.Path("/api-keys", state.ID.ValueString())); err != nil && !client.IsNotFound(err) {
		apiErr(&resp.Diagnostics, "revoke API key "+state.ID.ValueString(), err)
	}
}

func (r *apiKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
