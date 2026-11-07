package provider

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
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
	_ resource.Resource                   = &templateResource{}
	_ resource.ResourceWithImportState    = &templateResource{}
	_ resource.ResourceWithValidateConfig = &templateResource{}
)

func NewTemplateResource() resource.Resource { return &templateResource{} }

type templateResource struct{ resourceBase }

type templateModel struct {
	ID                     types.String `tfsdk:"id"`
	Name                   types.String `tfsdk:"name"`
	Alias                  types.String `tfsdk:"alias"`
	Subject                types.String `tfsdk:"subject"`
	From                   types.String `tfsdk:"from"`
	HTML                   types.String `tfsdk:"html"`
	Text                   types.String `tfsdk:"text"`
	PreviewText            types.String `tfsdk:"preview_text"`
	TemplateEngine         types.String `tfsdk:"template_engine"`
	Variables              types.List   `tfsdk:"variables"`
	Publish                types.Bool   `tfsdk:"publish"`
	Status                 types.String `tfsdk:"status"`
	PublishedAt            types.String `tfsdk:"published_at"`
	CurrentVersionID       types.String `tfsdk:"current_version_id"`
	VersionNumber          types.Int64  `tfsdk:"version_number"`
	HasUnpublishedVersions types.Bool   `tfsdk:"has_unpublished_versions"`
	CreatedAt              types.String `tfsdk:"created_at"`
}

type templateVariableModel struct {
	Key           types.String `tfsdk:"key"`
	Type          types.String `tfsdk:"type"`
	FallbackValue types.String `tfsdk:"fallback_value"`
}

var templateVariableAttrTypes = map[string]attr.Type{
	"key":            types.StringType,
	"type":           types.StringType,
	"fallback_value": types.StringType,
}

func (r *templateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_template"
}

func (r *templateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	nonEmpty := []validator.String{stringvalidator.LengthAtLeast(1)}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An email template. Every change is saved as a new draft version; with " +
			"`publish = true` the draft is published so sends use it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Template id.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name":  schema.StringAttribute{Required: true, MarkdownDescription: "Template name.", Validators: nonEmpty},
			"alias": schema.StringAttribute{Optional: true, MarkdownDescription: "Unique alias usable in place of the id when sending.", Validators: nonEmpty},
			"subject": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Subject line (required to publish).", Validators: nonEmpty,
			},
			"from": schema.StringAttribute{
				Optional: true, Validators: nonEmpty,
				MarkdownDescription: "Default sender, for example `Acme <hello@mail.example.com>` (required to publish; its domain must be verified).",
			},
			"html":         schema.StringAttribute{Required: true, MarkdownDescription: "HTML body.", Validators: nonEmpty},
			"text":         schema.StringAttribute{Optional: true, MarkdownDescription: "Plain-text body.", Validators: nonEmpty},
			"preview_text": schema.StringAttribute{Optional: true, MarkdownDescription: "Inbox preview text.", Validators: nonEmpty},
			"template_engine": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("simple"),
				MarkdownDescription: "`simple` (default, `{{{VAR}}}` placeholders) or `handlebars`.",
				Validators:          []validator.String{stringvalidator.OneOf("simple", "handlebars")},
			},
			"variables": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Variables the template uses (at most 50).",
				Validators:          []validator.List{listvalidator.SizeAtMost(50)},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"key": schema.StringAttribute{Required: true, MarkdownDescription: "Variable name.", Validators: nonEmpty},
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "`string` or `number`; with the handlebars engine also `boolean`, `object` or `array`.",
						Validators:          []validator.String{stringvalidator.OneOf("string", "number", "boolean", "object", "array")},
					},
					"fallback_value": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Value used when a send leaves the variable out: text for `string`, a number for `number`, `true`/`false` for `boolean`, JSON for `object` and `array`.",
					},
				}},
			},
			"publish": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				MarkdownDescription: "Publish the draft after every change. When the template gets unpublished edits outside " +
					"Terraform, the next plan shows it being published again. Setting it back to `false` does not unpublish.",
			},
			"status":                   schema.StringAttribute{Computed: true, MarkdownDescription: "`draft` or `published`."},
			"published_at":             schema.StringAttribute{Computed: true, MarkdownDescription: "When the current published version was published."},
			"current_version_id":       schema.StringAttribute{Computed: true, MarkdownDescription: "Id of the version GET returns (the draft, else the published one)."},
			"version_number":           schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of that version."},
			"has_unpublished_versions": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether a draft newer than the published version exists."},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "When the template was created.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *templateResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m templateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.Publish.ValueBool() {
		if m.Subject.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("subject"), "Subject required to publish", "A template needs a subject before it can be published.")
		}
		if m.From.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("from"), "From required to publish", "A template needs a From address before it can be published.")
		}
	}
	if !m.From.IsNull() && !m.From.IsUnknown() {
		if _, err := parseFrom(m.From.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("from"), "Invalid From address", err.Error())
		}
	}
	if m.Variables.IsNull() || m.Variables.IsUnknown() || m.TemplateEngine.IsUnknown() {
		return
	}
	handlebars := m.TemplateEngine.ValueString() == "handlebars"
	var vars []templateVariableModel
	resp.Diagnostics.Append(m.Variables.ElementsAs(ctx, &vars, false)...)
	seen := map[string]bool{}
	for i, v := range vars {
		p := path.Root("variables").AtListIndex(i)
		if v.Type.IsUnknown() || v.Key.IsUnknown() {
			continue
		}
		typ := v.Type.ValueString()
		if !handlebars && typ != "string" && typ != "number" {
			resp.Diagnostics.AddAttributeError(p.AtName("type"), "Variable type needs handlebars",
				`boolean, object and array variables need template_engine = "handlebars".`)
		}
		if seen[v.Key.ValueString()] {
			resp.Diagnostics.AddAttributeError(p.AtName("key"), "Duplicate variable", "Variable "+v.Key.ValueString()+" is listed twice.")
		}
		seen[v.Key.ValueString()] = true
		if !v.FallbackValue.IsNull() && !v.FallbackValue.IsUnknown() {
			if _, err := encodeFallback(typ, v.FallbackValue.ValueString()); err != nil {
				resp.Diagnostics.AddAttributeError(p.AtName("fallback_value"), "Invalid fallback value", err.Error())
			}
		}
	}
}

func parseFrom(s string) (string, error) {
	a, err := mail.ParseAddress(s)
	if err != nil || !strings.Contains(a.Address, "@") {
		return "", fmt.Errorf("%q is not a valid From address", s)
	}
	return a.Address, nil
}

// body is the full content of the template: POST and PATCH both take it
// (PATCH merges it over the current version, so sending every field keeps
// the version exactly as configured; "" clears a field).
func (m *templateModel) body(ctx context.Context, diags *diag.Diagnostics) map[string]any {
	b := map[string]any{
		"name":            m.Name.ValueString(),
		"alias":           m.Alias.ValueString(),
		"subject":         m.Subject.ValueString(),
		"from":            m.From.ValueString(),
		"html":            m.HTML.ValueString(),
		"text":            m.Text.ValueString(),
		"preview_text":    m.PreviewText.ValueString(),
		"template_engine": m.TemplateEngine.ValueString(),
	}
	vars := []map[string]any{}
	if !m.Variables.IsNull() && !m.Variables.IsUnknown() {
		var vs []templateVariableModel
		diags.Append(m.Variables.ElementsAs(ctx, &vs, false)...)
		for _, v := range vs {
			item := map[string]any{"key": v.Key.ValueString(), "type": v.Type.ValueString()}
			if !v.FallbackValue.IsNull() {
				fb, err := encodeFallback(v.Type.ValueString(), v.FallbackValue.ValueString())
				if err != nil {
					diags.AddAttributeError(path.Root("variables"), "Invalid fallback value", err.Error())
				}
				item["fallback_value"] = fb
			}
			vars = append(vars, item)
		}
	}
	b["variables"] = vars
	return b
}

func templateToModel(ctx context.Context, t *client.Template, m *templateModel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(t.ID)
	m.Name = types.StringValue(t.Name)
	m.Alias = strValue(t.Alias)
	m.Subject = strValue(t.Subject)
	m.From = strValue(t.From)
	m.HTML = strValue(t.HTML)
	m.Text = strValue(t.Text)
	m.PreviewText = strValue(t.PreviewText)
	m.TemplateEngine = types.StringValue("simple")
	if t.TemplateEngine != nil && *t.TemplateEngine != "" {
		m.TemplateEngine = types.StringValue(*t.TemplateEngine)
	}

	var prior []templateVariableModel
	if !m.Variables.IsNull() && !m.Variables.IsUnknown() {
		diags.Append(m.Variables.ElementsAs(ctx, &prior, false)...)
	}
	if len(t.Variables) == 0 && (m.Variables.IsNull() || m.Variables.IsUnknown()) {
		m.Variables = types.ListNull(types.ObjectType{AttrTypes: templateVariableAttrTypes})
	} else {
		vars := make([]templateVariableModel, 0, len(t.Variables))
		for i, v := range t.Variables {
			p := types.StringNull()
			if i < len(prior) && prior[i].Key.ValueString() == v.Key {
				p = prior[i].FallbackValue
			}
			vars = append(vars, templateVariableModel{
				Key: types.StringValue(v.Key), Type: types.StringValue(v.Type),
				FallbackValue: decodeFallback(v.Type, v.FallbackValue, p),
			})
		}
		lv, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: templateVariableAttrTypes}, vars)
		diags.Append(d...)
		m.Variables = lv
	}

	m.Status = types.StringValue(t.Status)
	m.PublishedAt = strValue(t.PublishedAt)
	m.CurrentVersionID = strValue(t.CurrentVersionID)
	m.VersionNumber = types.Int64Null()
	if t.VersionNumber != nil {
		m.VersionNumber = types.Int64Value(*t.VersionNumber)
	}
	m.HasUnpublishedVersions = types.BoolValue(t.HasUnpublishedVersions)
	m.CreatedAt = types.StringValue(t.CreatedAt)
	return diags
}

func (r *templateResource) get(ctx context.Context, id string) (*client.Template, error) {
	var t client.Template
	if err := r.client.Get(ctx, client.Path("/templates", id), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *templateResource) publish(ctx context.Context, id string, diags *diag.Diagnostics) bool {
	if err := r.client.Post(ctx, client.Path("/templates", id, "publish"), nil, nil); err != nil {
		apiErr(diags, "publish template "+id, err)
		return false
	}
	return true
}

func (r *templateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan templateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := plan.body(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, k := range []string{"alias", "subject", "from", "text", "preview_text"} {
		if body[k] == "" {
			delete(body, k)
		}
	}
	var created client.Created
	if err := r.client.Post(ctx, "/templates", body, &created); err != nil {
		apiErr(&resp.Diagnostics, "create template", err)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), created.ID)...)
	if plan.Publish.ValueBool() && !r.publish(ctx, created.ID, &resp.Diagnostics) {
		return
	}
	t, err := r.get(ctx, created.ID)
	if err != nil {
		apiErr(&resp.Diagnostics, "read template "+created.ID, err)
		return
	}
	publish := plan.Publish
	resp.Diagnostics.Append(templateToModel(ctx, t, &plan)...)
	plan.Publish = publish
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *templateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state templateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	t, err := r.get(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read template "+state.ID.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(templateToModel(ctx, t, &state)...)
	switch {
	case state.Publish.IsNull():
		state.Publish = types.BoolValue(t.Status == "published" && !t.HasUnpublishedVersions) // import
	case state.Publish.ValueBool() && (t.Status != "published" || t.HasUnpublishedVersions):
		// Edited outside Terraform and not published: plan to publish again.
		state.Publish = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *templateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state templateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	newBody := plan.body(ctx, &resp.Diagnostics)
	oldBody := state.body(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	changed := !jsonEqual(newBody, oldBody)
	if changed {
		if err := r.client.Patch(ctx, client.Path("/templates", id), newBody, nil); err != nil {
			apiErr(&resp.Diagnostics, "update template "+id, err)
			return
		}
	}
	if plan.Publish.ValueBool() && (changed || !state.Publish.ValueBool() || state.HasUnpublishedVersions.ValueBool()) {
		if !r.publish(ctx, id, &resp.Diagnostics) {
			return
		}
	}
	t, err := r.get(ctx, id)
	if err != nil {
		apiErr(&resp.Diagnostics, "read template "+id, err)
		return
	}
	publish := plan.Publish
	resp.Diagnostics.Append(templateToModel(ctx, t, &plan)...)
	plan.Publish = publish
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *templateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state templateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, client.Path("/templates", state.ID.ValueString())); err != nil && !client.IsNotFound(err) {
		apiErr(&resp.Diagnostics, "delete template "+state.ID.ValueString(), err)
	}
}

// ImportState takes a template id or alias.
func (r *templateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if !isUUID(id) {
		t, err := r.get(ctx, id)
		if err != nil {
			apiErr(&resp.Diagnostics, "look up template "+id, err)
			return
		}
		id = t.ID
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
