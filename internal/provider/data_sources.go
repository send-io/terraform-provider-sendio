package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/send-io/terraform-provider-sendio/internal/client"
)

// ---------------------------------------------------------------------------
// data "sendio_domain"
// ---------------------------------------------------------------------------

var (
	_ datasource.DataSource                     = &domainDataSource{}
	_ datasource.DataSourceWithConfigValidators = &domainDataSource{}
)

func NewDomainDataSource() datasource.DataSource { return &domainDataSource{} }

type domainDataSource struct{ dataSourceBase }

func (d *domainDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domain"
}

func (d *domainDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up one domain by id or name, with its DNS records.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Domain id. Set `id` or `name`."},
			"name":               schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Domain name. Set `id` or `name`."},
			"region":             str("Sending region."),
			"custom_return_path": str("Return-Path subdomain label."),
			"open_tracking":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether opens are tracked."},
			"click_tracking":     schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether clicks are tracked."},
			"tracking_subdomain": str("Subdomain label for tracked links."),
			"status":             str("Verification status."),
			"tracking_status":    str("Status of the tracking host."),
			"created_at":         str("When the domain was added."),
			"records": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "DNS records to publish.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"record":   str("What the record is for."),
					"type":     str("DNS record type."),
					"name":     str("Record name."),
					"value":    str("Record value."),
					"ttl":      str("Suggested TTL."),
					"priority": schema.Int64Attribute{Computed: true, MarkdownDescription: "MX priority."},
					"status":   str("Verification status of this record."),
				}},
			},
		},
	}
}

func (d *domainDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name")),
	}
}

func (d *domainDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m domainModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := m.ID.ValueString()
	if m.ID.IsNull() {
		found, err := findDomainByName(ctx, d.client, m.Name.ValueString())
		if err != nil {
			apiErr(&resp.Diagnostics, "list domains", err)
			return
		}
		if found == "" {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Domain not found", "No domain named "+m.Name.ValueString()+" exists in this team.")
			return
		}
		id = found
	}
	var dom client.Domain
	if err := d.client.Get(ctx, client.Path("/domains", id), &dom); err != nil {
		apiErr(&resp.Diagnostics, "read domain "+id, err)
		return
	}
	resp.Diagnostics.Append(domainToModel(ctx, &dom, &m)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------------------------------------------------------------------
// data "sendio_domains"
// ---------------------------------------------------------------------------

func NewDomainsDataSource() datasource.DataSource { return &domainsDataSource{} }

type domainsDataSource struct{ dataSourceBase }

type domainSummary struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Status    types.String `tfsdk:"status"`
	Region    types.String `tfsdk:"region"`
	CreatedAt types.String `tfsdk:"created_at"`
}

type domainsModel struct {
	Domains []domainSummary `tfsdk:"domains"`
}

func (d *domainsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_domains"
}

func (d *domainsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the team's sending domains, newest first.",
		Attributes: map[string]schema.Attribute{
			"domains": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The domains.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id": str("Domain id."), "name": str("Domain name."), "status": str("Verification status."),
					"region": str("Sending region."), "created_at": str("When the domain was added."),
				}},
			},
		},
	}
}

func (d *domainsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	var page struct {
		Data []client.Domain `json:"data"`
	}
	if err := d.client.Get(ctx, "/domains", &page); err != nil {
		apiErr(&resp.Diagnostics, "list domains", err)
		return
	}
	m := domainsModel{Domains: []domainSummary{}}
	for _, x := range page.Data {
		m.Domains = append(m.Domains, domainSummary{
			ID: types.StringValue(x.ID), Name: types.StringValue(x.Name), Status: types.StringValue(x.Status),
			Region: types.StringValue(x.Region), CreatedAt: types.StringValue(x.CreatedAt),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------------------------------------------------------------------
// data "sendio_templates"
// ---------------------------------------------------------------------------

func NewTemplatesDataSource() datasource.DataSource { return &templatesDataSource{} }

type templatesDataSource struct{ dataSourceBase }

type templateSummary struct {
	ID                     types.String `tfsdk:"id"`
	Name                   types.String `tfsdk:"name"`
	Alias                  types.String `tfsdk:"alias"`
	Status                 types.String `tfsdk:"status"`
	PublishedAt            types.String `tfsdk:"published_at"`
	CurrentVersionID       types.String `tfsdk:"current_version_id"`
	HasUnpublishedVersions types.Bool   `tfsdk:"has_unpublished_versions"`
	CreatedAt              types.String `tfsdk:"created_at"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
}

type templatesModel struct {
	Templates []templateSummary `tfsdk:"templates"`
}

func (d *templatesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_templates"
}

func (d *templatesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the team's templates, newest first (every page).",
		Attributes: map[string]schema.Attribute{
			"templates": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The templates.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id": str("Template id."), "name": str("Template name."), "alias": str("Template alias."),
					"status": str("`draft` or `published`."), "published_at": str("When it was last published."),
					"current_version_id":       str("Id of the current version."),
					"has_unpublished_versions": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether a newer draft exists."},
					"created_at":               str("When the template was created."), "updated_at": str("When it was last changed."),
				}},
			},
		},
	}
}

func (d *templatesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	items, err := client.ListAll(ctx, d.client, "/templates", func(t client.Template) string { return t.ID })
	if err != nil {
		apiErr(&resp.Diagnostics, "list templates", err)
		return
	}
	m := templatesModel{Templates: []templateSummary{}}
	for _, t := range items {
		m.Templates = append(m.Templates, templateSummary{
			ID: types.StringValue(t.ID), Name: types.StringValue(t.Name), Alias: strValue(t.Alias), Status: types.StringValue(t.Status),
			PublishedAt: strValue(t.PublishedAt), CurrentVersionID: strValue(t.CurrentVersionID),
			HasUnpublishedVersions: types.BoolValue(t.HasUnpublishedVersions), CreatedAt: types.StringValue(t.CreatedAt), UpdatedAt: strValue(t.UpdatedAt),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------------------------------------------------------------------
// data "sendio_api_keys"
// ---------------------------------------------------------------------------

func NewAPIKeysDataSource() datasource.DataSource { return &apiKeysDataSource{} }

type apiKeysDataSource struct{ dataSourceBase }

type apiKeySummary struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Permission  types.String `tfsdk:"permission"`
	DomainID    types.String `tfsdk:"domain_id"`
	TokenPrefix types.String `tfsdk:"token_prefix"`
	CreatedAt   types.String `tfsdk:"created_at"`
	LastUsedAt  types.String `tfsdk:"last_used_at"`
}

type apiKeysModel struct {
	APIKeys []apiKeySummary `tfsdk:"api_keys"`
}

func (d *apiKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_keys"
}

func (d *apiKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the team's active API keys. Tokens are never returned, only their prefixes.",
		Attributes: map[string]schema.Attribute{
			"api_keys": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "The API keys.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id": str("API key id."), "name": str("Key name."), "permission": str("`full_access` or `sending_access`."),
					"domain_id": str("Domain a sending key is limited to."), "token_prefix": str("First characters of the token."),
					"created_at": str("When the key was created."), "last_used_at": str("When the key was last used."),
				}},
			},
		},
	}
}

func (d *apiKeysDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	var page struct {
		Data []client.APIKey `json:"data"`
	}
	if err := d.client.Get(ctx, "/api-keys", &page); err != nil { // every key when no limit is given
		apiErr(&resp.Diagnostics, "list API keys", err)
		return
	}
	m := apiKeysModel{APIKeys: []apiKeySummary{}}
	for _, k := range page.Data {
		m.APIKeys = append(m.APIKeys, apiKeySummary{
			ID: types.StringValue(k.ID), Name: types.StringValue(k.Name), Permission: types.StringValue(k.Permission),
			DomainID: strValue(k.DomainID), TokenPrefix: types.StringValue(k.TokenPrefix),
			CreatedAt: types.StringValue(k.CreatedAt), LastUsedAt: strValue(k.LastUsedAt),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
