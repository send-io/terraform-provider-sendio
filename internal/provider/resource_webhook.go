package provider

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                = &webhookResource{}
	_ resource.ResourceWithImportState = &webhookResource{}
)

// webhookEvents mirrors the backend's events map (resources.go).
var webhookEvents = []string{
	"email.sent", "email.delivered", "email.delivery_delayed", "email.complained", "email.bounced", "email.opened",
	"email.clicked", "email.failed", "email.scheduled", "email.suppressed", "email.unsubscribed", "email.received",
	"email.replied", "message.received", "message.received.spam", "message.sent", "message.delivered", "message.bounced",
	"message.complained", "message.rejected", "message.opened", "domain.verified", "domain.created", "domain.updated",
	"domain.deleted", "suppression.added", "suppression.removed", "contact.created", "contact.updated", "contact.deleted",
	"contact.unsubscribed", "contact.list_unsubscribed", "contact.topics.updated", "topic.created", "topic.updated",
	"topic.deleted", "automation.run.started", "automation.run.completed", "automation.run.failed",
	"automation.run.cancelled", "automation.auto_paused",
}

// webhookFormats are the registered payload formats (webhook_format*.go).
var webhookFormats = []string{"sendio", "brevo", "mailchimp", "mailgun", "mailjet", "postmark", "sendgrid", "norender"}

// reservedHeaders cannot be set as custom webhook headers.
var reservedHeaders = map[string]bool{"content-type": true, "content-length": true, "host": true, "user-agent": true, "transfer-encoding": true, "connection": true}

func NewWebhookResource() resource.Resource { return &webhookResource{} }

type webhookResource struct{ resourceBase }

type webhookModel struct {
	ID            types.String `tfsdk:"id"`
	Endpoint      types.String `tfsdk:"endpoint"`
	Events        types.Set    `tfsdk:"events"`
	Format        types.String `tfsdk:"format"`
	Headers       types.Map    `tfsdk:"headers"`
	Status        types.String `tfsdk:"status"`
	SigningSecret types.String `tfsdk:"signing_secret"`
	CreatedAt     types.String `tfsdk:"created_at"`
}

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A webhook endpoint that receives send.io events, signed with `signing_secret`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Webhook id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"endpoint": schema.StringAttribute{
				Required: true, MarkdownDescription: "Public HTTPS URL that receives the events.",
				Validators: []validator.String{stringvalidator.RegexMatches(httpsURLRE, "must be an https:// URL")},
			},
			"events": schema.SetAttribute{
				Required: true, ElementType: types.StringType,
				MarkdownDescription: "Events to deliver, for example `email.delivered` and `email.bounced`.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(webhookEvents...)),
				},
			},
			"format": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("sendio"),
				MarkdownDescription: "Payload format: `sendio` (default), or a provider format (`postmark`, `sendgrid`, `mailgun`, `mailchimp`, `brevo`, `mailjet`) for code written against that provider.",
				Validators:          []validator.String{stringvalidator.OneOf(webhookFormats...)},
			},
			"headers": schema.MapAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Extra request headers sent with every delivery, `{name = value}` (at most 50).",
				Validators:          []validator.Map{headersValidator{}},
			},
			"status": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("enabled"),
				MarkdownDescription: "`enabled` (default) or `disabled`.",
				Validators:          []validator.String{stringvalidator.OneOf("enabled", "disabled")},
			},
			"signing_secret": schema.StringAttribute{
				Computed: true, Sensitive: true,
				MarkdownDescription: "Secret (`whsec_...`) for checking delivery signatures. Rotating it outside Terraform shows up on the next refresh.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "When the webhook was created.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (m *webhookModel) eventsList(ctx context.Context, diags *diag.Diagnostics) []string {
	var ev []string
	diags.Append(m.Events.ElementsAs(ctx, &ev, false)...)
	sort.Strings(ev)
	return ev
}

func (m *webhookModel) headersMap(ctx context.Context, diags *diag.Diagnostics) map[string]string {
	h := map[string]string{}
	if !m.Headers.IsNull() && !m.Headers.IsUnknown() {
		diags.Append(m.Headers.ElementsAs(ctx, &h, false)...)
	}
	return h
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{
		"endpoint": plan.Endpoint.ValueString(),
		"events":   plan.eventsList(ctx, &resp.Diagnostics),
		"format":   plan.Format.ValueString(),
		"headers":  plan.headersMap(ctx, &resp.Diagnostics),
	}
	if resp.Diagnostics.HasError() {
		return
	}
	var w client.Webhook
	if err := r.client.Post(ctx, "/webhooks", body, &w); err != nil {
		apiErr(&resp.Diagnostics, "create webhook", err)
		return
	}
	// Save the id now so a failure below leaves a resource Terraform can
	// still refresh and destroy.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), w.ID)...)
	if plan.Status.ValueString() == "disabled" {
		if err := r.client.Patch(ctx, client.Path("/webhooks", w.ID), map[string]any{"status": "disabled"}, nil); err != nil {
			apiErr(&resp.Diagnostics, "disable webhook "+w.ID, err)
			return
		}
	}
	if err := r.client.Get(ctx, client.Path("/webhooks", w.ID), &w); err != nil {
		apiErr(&resp.Diagnostics, "read webhook "+w.ID, err)
		return
	}
	resp.Diagnostics.Append(webhookToModel(ctx, &w, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func webhookToModel(ctx context.Context, w *client.Webhook, m *webhookModel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(w.ID)
	m.Endpoint = types.StringValue(w.Endpoint)
	ev, d := types.SetValueFrom(ctx, types.StringType, w.Events)
	diags.Append(d...)
	m.Events = ev
	m.Format = types.StringValue(w.Format)
	// No headers is {} from the API; keep null in state when the
	// configuration leaves headers out.
	if len(w.Headers) == 0 {
		if m.Headers.IsNull() || m.Headers.IsUnknown() || len(m.Headers.Elements()) > 0 {
			m.Headers = types.MapNull(types.StringType)
		} // else: an explicit headers = {} stays as written
	} else {
		h, d := types.MapValueFrom(ctx, types.StringType, w.Headers)
		diags.Append(d...)
		m.Headers = h
	}
	m.Status = types.StringValue(w.Status)
	m.SigningSecret = types.StringValue(w.SigningSecret)
	m.CreatedAt = types.StringValue(w.CreatedAt)
	return diags
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var w client.Webhook
	if err := r.client.Get(ctx, client.Path("/webhooks", state.ID.ValueString()), &w); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read webhook "+state.ID.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(webhookToModel(ctx, &w, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update sends only what changed; PATCH /webhooks/{id} leaves absent fields
// alone and replaces headers as a whole.
func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	body := map[string]any{}
	if !plan.Endpoint.Equal(state.Endpoint) {
		body["endpoint"] = plan.Endpoint.ValueString()
	}
	if !plan.Events.Equal(state.Events) {
		body["events"] = plan.eventsList(ctx, &resp.Diagnostics)
	}
	if !plan.Format.Equal(state.Format) {
		body["format"] = plan.Format.ValueString()
	}
	if !plan.Headers.Equal(state.Headers) {
		body["headers"] = plan.headersMap(ctx, &resp.Diagnostics)
	}
	if !plan.Status.Equal(state.Status) {
		body["status"] = plan.Status.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if len(body) > 0 {
		if err := r.client.Patch(ctx, client.Path("/webhooks", id), body, nil); err != nil {
			apiErr(&resp.Diagnostics, "update webhook "+id, err)
			return
		}
	}
	var w client.Webhook
	if err := r.client.Get(ctx, client.Path("/webhooks", id), &w); err != nil {
		apiErr(&resp.Diagnostics, "read webhook "+id, err)
		return
	}
	resp.Diagnostics.Append(webhookToModel(ctx, &w, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, client.Path("/webhooks", state.ID.ValueString())); err != nil && !client.IsNotFound(err) {
		apiErr(&resp.Diagnostics, "delete webhook "+state.ID.ValueString(), err)
	}
}

func (r *webhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
