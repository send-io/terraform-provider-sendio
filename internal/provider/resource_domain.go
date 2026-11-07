package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/send-io/terraform-provider-sendio/internal/client"
)

var (
	_ resource.Resource                = &domainResource{}
	_ resource.ResourceWithImportState = &domainResource{}
)

func NewDomainResource() resource.Resource { return &domainResource{} }

type domainResource struct{ resourceBase }

type domainModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Region            types.String `tfsdk:"region"`
	CustomReturnPath  types.String `tfsdk:"custom_return_path"`
	OpenTracking      types.Bool   `tfsdk:"open_tracking"`
	ClickTracking     types.Bool   `tfsdk:"click_tracking"`
	TrackingSubdomain types.String `tfsdk:"tracking_subdomain"`
	Status            types.String `tfsdk:"status"`
	TrackingStatus    types.String `tfsdk:"tracking_status"`
	CreatedAt         types.String `tfsdk:"created_at"`
	Records           types.List   `tfsdk:"records"`
}

var domainRegions = []string{"us-east-1", "eu-west-1", "sa-east-1", "ap-northeast-1"}

var recordAttrTypes = map[string]attr.Type{
	"record":   types.StringType,
	"type":     types.StringType,
	"name":     types.StringType,
	"value":    types.StringType,
	"ttl":      types.StringType,
	"priority": types.Int64Type,
	"status":   types.StringType,
}

type recordModel struct {
	Record   types.String `tfsdk:"record"`
	Type     types.String `tfsdk:"type"`
	Name     types.String `tfsdk:"name"`
	Value    types.String `tfsdk:"value"`
	TTL      types.String `tfsdk:"ttl"`
	Priority types.Int64  `tfsdk:"priority"`
	Status   types.String `tfsdk:"status"`
}

func recordsValue(ctx context.Context, recs []client.DomainRecord) (types.List, diag.Diagnostics) {
	out := make([]recordModel, 0, len(recs))
	for _, r := range recs {
		m := recordModel{
			Record: types.StringValue(r.Record), Type: types.StringValue(r.Type), Name: types.StringValue(r.Name),
			Value: types.StringValue(r.Value), TTL: strValue(r.TTL), Status: strValue(r.Status), Priority: types.Int64Null(),
		}
		if r.Priority != nil {
			m.Priority = types.Int64Value(*r.Priority)
		}
		out = append(out, m)
	}
	return types.ListValueFrom(ctx, types.ObjectType{AttrTypes: recordAttrTypes}, out)
}

func recordsSchemaAttribute() schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "DNS records to publish for the domain, with their verification status.",
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"record":   schema.StringAttribute{Computed: true, MarkdownDescription: "What the record is for: `SPF`, `DKIM`, `Tracking`, `Receiving MX`, ..."},
			"type":     schema.StringAttribute{Computed: true, MarkdownDescription: "DNS record type (`MX`, `TXT`, `CNAME`, `CAA`)."},
			"name":     schema.StringAttribute{Computed: true, MarkdownDescription: "Record name, relative to the domain."},
			"value":    schema.StringAttribute{Computed: true, MarkdownDescription: "Record value."},
			"ttl":      schema.StringAttribute{Computed: true, MarkdownDescription: "Suggested TTL (`Auto` when any TTL works)."},
			"priority": schema.Int64Attribute{Computed: true, MarkdownDescription: "MX priority."},
			"status":   schema.StringAttribute{Computed: true, MarkdownDescription: "Verification status of this record."},
		}},
	}
}

func (r *domainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (r *domainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A sending domain. Creating it returns the DNS records to publish; use " +
			"`sendio_domain_verification` to check them and wait until the domain is verified.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Domain id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Domain name, lower case (for example `mail.example.com`). Changing it creates a new domain.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthAtMost(253),
					stringvalidator.RegexMatches(domainNameRE, "must be a lower-case domain name such as mail.example.com"),
				},
			},
			"region": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("us-east-1"),
				MarkdownDescription: "Sending region: `us-east-1` (default), `eu-west-1`, `sa-east-1` or `ap-northeast-1`. Changing it creates a new domain.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.OneOf(domainRegions...)},
			},
			"custom_return_path": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("send"),
				MarkdownDescription: "Return-Path subdomain label (default `send`). Changing it creates a new domain.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(returnPathRE, "must start with a letter and use a-z, 0-9 and dashes (at most 63 characters)")},
			},
			"open_tracking": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Track opens of mail sent from this domain.",
			},
			"click_tracking": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Track link clicks in mail sent from this domain.",
			},
			"tracking_subdomain": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Subdomain label for tracked links (for example `links`). It can be changed in place, but the " +
					"API cannot remove it, so removing it from the configuration replaces the domain.",
				Validators: []validator.String{stringvalidator.RegexMatches(trackingLabelRE, "must be a single lower-case DNS label (a-z, 0-9 and dashes)")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = !req.StateValue.IsNull() && req.PlanValue.IsNull()
					},
					"Removing the tracking subdomain replaces the domain (the API can change it but not remove it).",
					"Removing the tracking subdomain replaces the domain (the API can change it but not remove it).",
				)},
			},
			"status": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Verification status: `not_started`, `pending`, `partially_verified`, `verified`, `failed`, ...",
			},
			"tracking_status": schema.StringAttribute{Computed: true, MarkdownDescription: "Status of the tracking host (`off`, `pending`, `active`, ...)."},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "When the domain was added.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"records": recordsSchemaAttribute(),
		},
	}
}

func (r *domainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{
		"name":               plan.Name.ValueString(),
		"region":             plan.Region.ValueString(),
		"custom_return_path": plan.CustomReturnPath.ValueString(),
		"open_tracking":      plan.OpenTracking.ValueBool(),
		"click_tracking":     plan.ClickTracking.ValueBool(),
	}
	if !plan.TrackingSubdomain.IsNull() {
		body["tracking_subdomain"] = plan.TrackingSubdomain.ValueString()
	}
	var d client.Domain
	if err := r.client.Post(ctx, "/domains", body, &d); err != nil {
		apiErr(&resp.Diagnostics, "create domain "+plan.Name.ValueString(), err)
		return
	}
	// Re-read: the create response omits nothing today, but GET is the
	// shape Read uses, so state is the same either way.
	if err := r.client.Get(ctx, client.Path("/domains", d.ID), &d); err != nil {
		apiErr(&resp.Diagnostics, "read domain "+d.ID, err)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), d.ID)...)
		return
	}
	resp.Diagnostics.Append(domainToModel(ctx, &d, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func domainToModel(ctx context.Context, d *client.Domain, m *domainModel) diag.Diagnostics {
	m.ID = types.StringValue(d.ID)
	m.Name = types.StringValue(d.Name)
	m.Region = types.StringValue(d.Region)
	m.CustomReturnPath = types.StringValue(d.CustomReturnPath)
	m.OpenTracking = types.BoolValue(d.OpenTracking)
	m.ClickTracking = types.BoolValue(d.ClickTracking)
	m.TrackingSubdomain = strValue(d.TrackingSubdomain)
	m.Status = types.StringValue(d.Status)
	m.TrackingStatus = strValue(d.TrackingStatus)
	m.CreatedAt = types.StringValue(d.CreatedAt)
	recs, diags := recordsValue(ctx, d.Records)
	m.Records = recs
	return diags
}

func (r *domainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var d client.Domain
	if err := r.client.Get(ctx, client.Path("/domains", state.ID.ValueString()), &d); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read domain "+state.ID.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(domainToModel(ctx, &d, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *domainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state domainModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	body := map[string]any{}
	if !plan.OpenTracking.Equal(state.OpenTracking) {
		body["open_tracking"] = plan.OpenTracking.ValueBool()
	}
	if !plan.ClickTracking.Equal(state.ClickTracking) {
		body["click_tracking"] = plan.ClickTracking.ValueBool()
	}
	if !plan.TrackingSubdomain.IsNull() && !plan.TrackingSubdomain.Equal(state.TrackingSubdomain) {
		body["tracking_subdomain"] = plan.TrackingSubdomain.ValueString()
	}
	if len(body) > 0 {
		if err := r.client.Patch(ctx, client.Path("/domains", id), body, nil); err != nil {
			apiErr(&resp.Diagnostics, "update domain "+id, err)
			return
		}
	}
	var d client.Domain
	if err := r.client.Get(ctx, client.Path("/domains", id), &d); err != nil {
		apiErr(&resp.Diagnostics, "read domain "+id, err)
		return
	}
	resp.Diagnostics.Append(domainToModel(ctx, &d, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *domainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state domainModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, client.Path("/domains", state.ID.ValueString())); err != nil && !client.IsNotFound(err) {
		apiErr(&resp.Diagnostics, "delete domain "+state.ID.ValueString(), err)
	}
}

// ImportState takes a domain id or a domain name.
func (r *domainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if !isUUID(id) {
		found, err := findDomainByName(ctx, r.client, id)
		if err != nil {
			apiErr(&resp.Diagnostics, "look up domain "+id, err)
			return
		}
		if found == "" {
			resp.Diagnostics.AddError("Domain not found", "No domain named "+id+" exists in this team.")
			return
		}
		id = found
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func findDomainByName(ctx context.Context, c *client.Client, name string) (string, error) {
	var page struct {
		Data []client.Domain `json:"data"`
	}
	// GET /domains returns every domain when no limit is given.
	if err := c.Get(ctx, "/domains", &page); err != nil {
		return "", err
	}
	for _, d := range page.Data {
		if strings.EqualFold(d.Name, name) {
			return d.ID, nil
		}
	}
	return "", nil
}
