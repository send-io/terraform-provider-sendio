// Package provider implements the send.io Terraform provider.
package provider

import (
	"context"
	"net/url"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/send-io/terraform-provider-sendio/internal/client"
)

var _ provider.Provider = &sendioProvider{}

type sendioProvider struct {
	version string
}

// New returns the provider factory for providerserver.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &sendioProvider{version: version} }
}

type providerModel struct {
	APIKey  types.String `tfsdk:"api_key"`
	BaseURL types.String `tfsdk:"base_url"`
}

func (p *sendioProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "sendio"
	resp.Version = p.version
}

func (p *sendioProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage send.io domains, webhooks, API keys, templates, audiences and agent inboxes.",
		MarkdownDescription: "Manage [send.io](https://send.io) domains, webhooks, API keys, templates, " +
			"marketing objects (segments, topics, contact properties), suppressions and agent inboxes.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "A send.io API key with full access. Defaults to the `SENDIO_API_KEY` environment variable.",
			},
			"base_url": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "API base URL. Defaults to the `SENDIO_BASE_URL` environment variable, else " +
					"`https://api.send.io`.",
			},
		},
	}
}

func (p *sendioProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Unknown send.io API key",
			"The api_key value is not known until apply. Set it to a static value or use the SENDIO_API_KEY environment variable.")
	}
	if cfg.BaseURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Unknown send.io base URL",
			"The base_url value is not known until apply. Set it to a static value or use the SENDIO_BASE_URL environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	apiKey := os.Getenv("SENDIO_API_KEY")
	if !cfg.APIKey.IsNull() {
		apiKey = cfg.APIKey.ValueString()
	}
	baseURL := os.Getenv("SENDIO_BASE_URL")
	if !cfg.BaseURL.IsNull() {
		baseURL = cfg.BaseURL.ValueString()
	}
	if baseURL == "" {
		baseURL = client.DefaultBaseURL
	}
	if strings.TrimSpace(apiKey) == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing send.io API key",
			"Set api_key in the provider block or the SENDIO_API_KEY environment variable.")
	}
	if u, err := url.Parse(baseURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Invalid send.io base URL",
			"base_url must be an absolute http(s) URL such as https://api.send.io.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	c := client.New(baseURL, apiKey, "terraform-provider-sendio/"+p.version)
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *sendioProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewDomainResource,
		NewDomainVerificationResource,
		NewWebhookResource,
		NewAPIKeyResource,
		NewSuppressionResource,
		NewTemplateResource,
		NewSegmentResource,
		NewTopicResource,
		NewContactPropertyResource,
		NewInboxResource,
	}
}

func (p *sendioProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewDomainDataSource,
		NewDomainsDataSource,
		NewTemplatesDataSource,
		NewAPIKeysDataSource,
	}
}
