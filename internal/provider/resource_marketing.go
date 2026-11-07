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

func idAttribute(what string) schema.StringAttribute {
	return schema.StringAttribute{
		Computed: true, MarkdownDescription: what + " id.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func createdAtAttribute(what string) schema.StringAttribute {
	return schema.StringAttribute{
		Computed: true, MarkdownDescription: "When the " + what + " was created.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func deleteByID(ctx context.Context, c *client.Client, base, id, what string, resp *resource.DeleteResponse) {
	if err := c.Delete(ctx, client.Path(base, id)); err != nil && !client.IsNotFound(err) {
		apiErr(&resp.Diagnostics, "delete "+what+" "+id, err)
	}
}

// ---------------------------------------------------------------------------
// sendio_segment
// ---------------------------------------------------------------------------

var (
	_ resource.Resource                = &segmentResource{}
	_ resource.ResourceWithImportState = &segmentResource{}
)

func NewSegmentResource() resource.Resource { return &segmentResource{} }

type segmentResource struct{ resourceBase }

type segmentModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	CreatedAt types.String `tfsdk:"created_at"`
}

func (r *segmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_segment"
}

func (r *segmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A segment: a static list of contacts that broadcasts can target. Destroying it keeps the contacts.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Segment"),
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Segment name. Renaming updates it in place.",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1), notPadded{}},
			},
			"created_at": createdAtAttribute("segment"),
		},
	}
}

func (r *segmentResource) read(ctx context.Context, id string, m *segmentModel) error {
	var s client.Segment
	if err := r.client.Get(ctx, client.Path("/segments", id), &s); err != nil {
		return err
	}
	m.ID, m.Name, m.CreatedAt = types.StringValue(s.ID), types.StringValue(s.Name), types.StringValue(s.CreatedAt)
	return nil
}

func (r *segmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan segmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var created client.Created
	if err := r.client.Post(ctx, "/segments", map[string]any{"name": plan.Name.ValueString()}, &created); err != nil {
		apiErr(&resp.Diagnostics, "create segment", err)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), created.ID)...)
	if err := r.read(ctx, created.ID, &plan); err != nil {
		apiErr(&resp.Diagnostics, "read segment "+created.ID, err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *segmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state segmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.read(ctx, state.ID.ValueString(), &state); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read segment "+state.ID.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *segmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state segmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.client.Patch(ctx, client.Path("/segments", id), map[string]any{"name": plan.Name.ValueString()}, nil); err != nil {
		apiErr(&resp.Diagnostics, "rename segment "+id, err)
		return
	}
	if err := r.read(ctx, id, &plan); err != nil {
		apiErr(&resp.Diagnostics, "read segment "+id, err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *segmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state segmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if !resp.Diagnostics.HasError() {
		deleteByID(ctx, r.client, "/segments", state.ID.ValueString(), "segment", resp)
	}
}

func (r *segmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ---------------------------------------------------------------------------
// sendio_topic
// ---------------------------------------------------------------------------

var (
	_ resource.Resource                = &topicResource{}
	_ resource.ResourceWithImportState = &topicResource{}
)

func NewTopicResource() resource.Resource { return &topicResource{} }

type topicResource struct{ resourceBase }

type topicModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Description         types.String `tfsdk:"description"`
	DefaultSubscription types.String `tfsdk:"default_subscription"`
	Visibility          types.String `tfsdk:"visibility"`
	CreatedAt           types.String `tfsdk:"created_at"`
}

func (r *topicResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_topic"
}

func (r *topicResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A topic contacts can subscribe to or opt out of (shown on the unsubscribe page when public).",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Topic"),
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Topic name (at most 50 characters).",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 50)},
			},
			"description": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Description (at most 200 characters).",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 200)},
			},
			"default_subscription": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "`opt_in`: every contact gets the topic unless they opt out; `opt_out`: only contacts who opt in. " +
					"It cannot be changed, so changing it creates a new topic.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf("opt_in", "opt_out")},
			},
			"visibility": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("private"),
				MarkdownDescription: "`private` (default) or `public` (listed on the unsubscribe page).",
				Validators:          []validator.String{stringvalidator.OneOf("public", "private")},
			},
			"created_at": createdAtAttribute("topic"),
		},
	}
}

func (r *topicResource) read(ctx context.Context, id string, m *topicModel) error {
	var t client.Topic
	if err := r.client.Get(ctx, client.Path("/topics", id), &t); err != nil {
		return err
	}
	m.ID, m.Name = types.StringValue(t.ID), types.StringValue(t.Name)
	m.Description = strValue(t.Description)
	m.DefaultSubscription = types.StringValue(t.DefaultSubscription)
	m.Visibility = types.StringValue(t.Visibility)
	m.CreatedAt = types.StringValue(t.CreatedAt)
	return nil
}

func (r *topicResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan topicModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{
		"name":                 plan.Name.ValueString(),
		"default_subscription": plan.DefaultSubscription.ValueString(),
		"visibility":           plan.Visibility.ValueString(),
	}
	if !plan.Description.IsNull() {
		body["description"] = plan.Description.ValueString()
	}
	var created client.Created
	if err := r.client.Post(ctx, "/topics", body, &created); err != nil {
		apiErr(&resp.Diagnostics, "create topic", err)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), created.ID)...)
	if err := r.read(ctx, created.ID, &plan); err != nil {
		apiErr(&resp.Diagnostics, "read topic "+created.ID, err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *topicResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state topicModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.read(ctx, state.ID.ValueString(), &state); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read topic "+state.ID.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update sends the changed fields; description "" clears it.
func (r *topicResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state topicModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	body := map[string]any{}
	if !plan.Name.Equal(state.Name) {
		body["name"] = plan.Name.ValueString()
	}
	if !plan.Description.Equal(state.Description) {
		body["description"] = plan.Description.ValueString()
	}
	if !plan.Visibility.Equal(state.Visibility) {
		body["visibility"] = plan.Visibility.ValueString()
	}
	if len(body) > 0 {
		if err := r.client.Patch(ctx, client.Path("/topics", id), body, nil); err != nil {
			apiErr(&resp.Diagnostics, "update topic "+id, err)
			return
		}
	}
	if err := r.read(ctx, id, &plan); err != nil {
		apiErr(&resp.Diagnostics, "read topic "+id, err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *topicResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state topicModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if !resp.Diagnostics.HasError() {
		deleteByID(ctx, r.client, "/topics", state.ID.ValueString(), "topic", resp)
	}
}

func (r *topicResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ---------------------------------------------------------------------------
// sendio_contact_property
// ---------------------------------------------------------------------------

var (
	_ resource.Resource                   = &contactPropertyResource{}
	_ resource.ResourceWithImportState    = &contactPropertyResource{}
	_ resource.ResourceWithValidateConfig = &contactPropertyResource{}
)

func NewContactPropertyResource() resource.Resource { return &contactPropertyResource{} }

type contactPropertyResource struct{ resourceBase }

type contactPropertyModel struct {
	ID            types.String `tfsdk:"id"`
	Key           types.String `tfsdk:"key"`
	Type          types.String `tfsdk:"type"`
	FallbackValue types.String `tfsdk:"fallback_value"`
	CreatedAt     types.String `tfsdk:"created_at"`
}

func (r *contactPropertyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_contact_property"
}

func (r *contactPropertyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A custom contact property (for example `company` or `plan`). Destroying it removes the value from every contact.",
		Attributes: map[string]schema.Attribute{
			"id": idAttribute("Contact property"),
			"key": schema.StringAttribute{
				Required: true, MarkdownDescription: "Property key: letters, digits and underscores, at most 50 characters. Changing it creates a new property.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.RegexMatches(propertyKeyRE, "must use letters, digits and underscores (at most 50 characters)")},
			},
			"type": schema.StringAttribute{
				Required: true, MarkdownDescription: "`string` or `number`. Changing it creates a new property.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf("string", "number")},
			},
			"fallback_value": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Value used where a contact has none (a number for `number` properties).",
			},
			"created_at": createdAtAttribute("contact property"),
		},
	}
}

func (r *contactPropertyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m contactPropertyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Type.IsUnknown() || m.FallbackValue.IsNull() || m.FallbackValue.IsUnknown() {
		return
	}
	if _, err := encodeFallback(m.Type.ValueString(), m.FallbackValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("fallback_value"), "Invalid fallback value", err.Error())
	}
}

func (m *contactPropertyModel) fallback() any {
	if m.FallbackValue.IsNull() {
		return nil
	}
	v, _ := encodeFallback(m.Type.ValueString(), m.FallbackValue.ValueString())
	return v
}

func (r *contactPropertyResource) read(ctx context.Context, id string, m *contactPropertyModel) error {
	var p client.ContactProperty
	if err := r.client.Get(ctx, client.Path("/contact-properties", id), &p); err != nil {
		return err
	}
	m.ID, m.Key, m.Type = types.StringValue(p.ID), types.StringValue(p.Key), types.StringValue(p.Type)
	m.FallbackValue = decodeFallback(p.Type, p.FallbackValue, m.FallbackValue)
	m.CreatedAt = types.StringValue(p.CreatedAt)
	return nil
}

func (r *contactPropertyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan contactPropertyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"key": plan.Key.ValueString(), "type": plan.Type.ValueString()}
	if fb := plan.fallback(); fb != nil {
		body["fallback_value"] = fb
	}
	var created client.Created
	if err := r.client.Post(ctx, "/contact-properties", body, &created); err != nil {
		apiErr(&resp.Diagnostics, "create contact property "+plan.Key.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), created.ID)...)
	if err := r.read(ctx, created.ID, &plan); err != nil {
		apiErr(&resp.Diagnostics, "read contact property "+created.ID, err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *contactPropertyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state contactPropertyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.read(ctx, state.ID.ValueString(), &state); err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		apiErr(&resp.Diagnostics, "read contact property "+state.ID.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update changes the fallback value (null clears it); key and type replace.
func (r *contactPropertyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state contactPropertyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.client.Patch(ctx, client.Path("/contact-properties", id), map[string]any{"fallback_value": plan.fallback()}, nil); err != nil {
		apiErr(&resp.Diagnostics, "update contact property "+id, err)
		return
	}
	if err := r.read(ctx, id, &plan); err != nil {
		apiErr(&resp.Diagnostics, "read contact property "+id, err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *contactPropertyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state contactPropertyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if !resp.Diagnostics.HasError() {
		deleteByID(ctx, r.client, "/contact-properties", state.ID.ValueString(), "contact property", resp)
	}
}

func (r *contactPropertyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
