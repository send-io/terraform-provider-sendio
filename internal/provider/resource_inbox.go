package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/send-io/terraform-provider-sendio/internal/client"
)

var (
	_ resource.Resource                = &inboxResource{}
	_ resource.ResourceWithImportState = &inboxResource{}
)

func NewInboxResource() resource.Resource { return &inboxResource{} }

type inboxResource struct{ resourceBase }

type inboxModel struct {
	ID          types.String `tfsdk:"id"`
	Username    types.String `tfsdk:"username"`
	Domain      types.String `tfsdk:"domain"`
	DisplayName types.String `tfsdk:"display_name"`
	ClientID    types.String `tfsdk:"client_id"`
	Address     types.String `tfsdk:"address"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (r *inboxResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inbox"
}

func (r *inboxResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An agent inbox: an email address an AI agent sends and receives with through the API. " +
			"It lives on one of your verified domains or, when `domain` is left out, on the shared agent domain " +
			"(no DNS work). Destroying it stops the address receiving; its threads stay readable.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Inbox"),
			"username": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Local part of the address (a-z, 0-9, dot, dash, underscore, plus). Generated when left out. Changing it creates a new inbox.",
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.RegexMatches(localPartRE, "must be a lower-case local part: a-z, 0-9, dot, dash, underscore and plus (1 to 64 characters)")},
			},
			"domain": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "A verified sending domain of yours, or a company subdomain of the shared agent domain. " +
					"Defaults to the shared agent domain. Changing it creates a new inbox.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.RegexMatches(domainNameRE, "must be a lower-case domain name")},
			},
			"display_name": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Sender name shown with the address (at most 100 characters, no quotes or angle brackets).",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 100), notPadded{},
					stringvalidator.RegexMatches(displayNameRE, "must not contain quotes, angle brackets or line breaks")},
			},
			"client_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Your own idempotency id: creating an inbox with a client_id that already exists returns " +
					"that inbox. Changing it creates a new inbox.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1), notPadded{}},
			},
			"address": schema.StringAttribute{
				Computed: true, MarkdownDescription: "The inbox address.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": createdAtAttribute("inbox"),
		},
	}
}

func inboxToModel(in *client.Inbox, m *inboxModel) {
	m.ID = types.StringValue(in.ID)
	m.Address = types.StringValue(in.Address)
	if at := strings.LastIndex(in.Address, "@"); at > 0 {
		m.Username = types.StringValue(in.Address[:at])
		m.Domain = types.StringValue(in.Address[at+1:])
	}
	m.DisplayName = optStr(deref(in.DisplayName))
	m.ClientID = strValue(in.ClientID)
	m.CreatedAt = types.StringValue(in.CreatedAt)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (r *inboxResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan inboxModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{}
	for k, v := range map[string]types.String{"username": plan.Username, "domain": plan.Domain, "display_name": plan.DisplayName, "client_id": plan.ClientID} {
		if !v.IsNull() && !v.IsUnknown() {
			body[k] = v.ValueString()
		}
	}
	var in client.Inbox
	if err := r.client.Post(ctx, "/inboxes", body, &in); err != nil {
		apiErr(&resp.Diagnostics, "create inbox", err)
		return
	}
	inboxToModel(&in, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inboxResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state inboxModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var in client.Inbox
	if err := r.client.Get(ctx, client.Path("/inboxes", state.ID.ValueString()), &in); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read inbox "+state.ID.ValueString(), err)
		return
	}
	inboxToModel(&in, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes display_name (PATCH /inboxes/{id}); "" clears it.
func (r *inboxResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state inboxModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	var in client.Inbox
	if err := r.client.Patch(ctx, client.Path("/inboxes", id), map[string]any{"display_name": plan.DisplayName.ValueString()}, &in); err != nil {
		apiErr(&resp.Diagnostics, "update inbox "+id, err)
		return
	}
	inboxToModel(&in, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inboxResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inboxModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if !resp.Diagnostics.HasError() {
		deleteByID(ctx, r.client, "/inboxes", state.ID.ValueString(), "inbox", resp)
	}
}

// ImportState takes an inbox id or address.
func (r *inboxResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if !isUUID(id) {
		var in client.Inbox
		if err := r.client.Get(ctx, client.Path("/inboxes", id), &in); err != nil {
			apiErr(&resp.Diagnostics, "look up inbox "+id, err)
			return
		}
		id = in.ID
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
