package provider

import (
	"context"
	"net/mail"
	"strings"

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
	_ resource.Resource                = &suppressionResource{}
	_ resource.ResourceWithImportState = &suppressionResource{}
)

var suppressionOrigins = []string{"manual", "bounce", "soft_bounce", "complaint", "invalid", "block"}

func NewSuppressionResource() resource.Resource { return &suppressionResource{} }

type suppressionResource struct{ resourceBase }

type suppressionModel struct {
	ID        types.String `tfsdk:"id"`
	Email     types.String `tfsdk:"email"`
	Origin    types.String `tfsdk:"origin"`
	Comment   types.String `tfsdk:"comment"`
	ExpiresAt types.String `tfsdk:"expires_at"`
	CreatedAt types.String `tfsdk:"created_at"`
}

func (r *suppressionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_suppression"
}

func (r *suppressionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An address on the team's suppression list: no email is sent to it until the " +
			"suppression is removed (destroyed) or expires.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Suppression id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"email": schema.StringAttribute{
				Required: true, MarkdownDescription: "The suppressed address. Changing it creates a new suppression.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{emailValidator{}},
			},
			"origin": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("manual"),
				MarkdownDescription: "Why the address is suppressed: `manual` (default), `bounce`, `soft_bounce` " +
					"(expires after 24 hours unless `expires_at` is set), `complaint`, `invalid` or `block`. Changing it creates a new suppression.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf(suppressionOrigins...)},
			},
			"comment": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Note kept with the suppression (at most 500 characters).",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 500)},
			},
			"expires_at": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "When the suppression lapses (RFC 3339, in the future). Changing it creates a new suppression.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplaceIfConfigured()},
				Validators:          []validator.String{rfc3339Validator{}},
			},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "When the address was suppressed.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *suppressionResource) upsert(ctx context.Context, m *suppressionModel, withOrigin bool) (*client.Suppression, error) {
	body := map[string]any{"email": m.Email.ValueString()}
	if withOrigin {
		body["origin"] = m.Origin.ValueString()
		if !m.ExpiresAt.IsNull() && !m.ExpiresAt.IsUnknown() {
			body["expires_at"] = m.ExpiresAt.ValueString()
		}
	}
	// comment "" clears a stored comment (the API stores nullif(comment,'')).
	body["comment"] = m.Comment.ValueString()
	var s client.Suppression
	if err := r.client.Post(ctx, "/suppressions", body, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *suppressionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan suppressionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.upsert(ctx, &plan, true)
	if err != nil {
		apiErr(&resp.Diagnostics, "suppress "+plan.Email.ValueString(), err)
		return
	}
	suppressionToModel(s, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func suppressionToModel(s *client.Suppression, m *suppressionModel) {
	m.ID = types.StringValue(s.ID)
	// The API may normalise the address ("Name <a@b>" to a@b, case); keep the
	// configured spelling when it names the same mailbox.
	if m.Email.IsNull() || m.Email.IsUnknown() || !strings.EqualFold(normalizeEmail(m.Email.ValueString()), s.Email) {
		m.Email = types.StringValue(s.Email)
	}
	m.Origin = types.StringValue(s.Origin)
	m.Comment = strValue(s.Comment)
	m.ExpiresAt = keepTime(m.ExpiresAt, s.ExpiresAt)
	m.CreatedAt = types.StringValue(s.CreatedAt)
}

func normalizeEmail(s string) string {
	if a, err := mail.ParseAddress(s); err == nil {
		return a.Address
	}
	return s
}

func (r *suppressionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state suppressionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var s client.Suppression
	if err := r.client.Get(ctx, client.Path("/suppressions", state.ID.ValueString())+"?include_expired=true", &s); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read suppression "+state.ID.ValueString(), err)
		return
	}
	suppressionToModel(&s, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the comment: POST /suppressions updates an existing
// suppression of the address with the fields given.
func (r *suppressionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state suppressionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Email = state.Email
	s, err := r.upsert(ctx, &plan, false)
	if err != nil {
		apiErr(&resp.Diagnostics, "update suppression "+state.ID.ValueString(), err)
		return
	}
	if s.ID != state.ID.ValueString() {
		resp.Diagnostics.AddWarning("Suppression replaced outside Terraform",
			"The address had a different active suppression ("+s.ID+"); Terraform now tracks that one.")
	}
	suppressionToModel(s, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *suppressionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state suppressionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, client.Path("/suppressions", state.ID.ValueString())); err != nil && !client.IsNotFound(err) {
		apiErr(&resp.Diagnostics, "remove suppression "+state.ID.ValueString(), err)
	}
}

// ImportState takes a suppression id or the suppressed address.
func (r *suppressionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if !isUUID(id) {
		var s client.Suppression
		if err := r.client.Get(ctx, client.Path("/suppressions", id)+"?include_expired=true", &s); err != nil {
			apiErr(&resp.Diagnostics, "look up suppression "+id, err)
			return
		}
		id = s.ID
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
