package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/send-io/terraform-provider-sendio/internal/client"
)

// base holds the configured API client for resources and data sources.
type base struct {
	client *client.Client
}

func (b *base) configure(data any, diags *diag.Diagnostics) {
	if data == nil {
		return // provider not configured yet (validation-only calls)
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T. Please report this issue.", data))
		return
	}
	b.client = c
}

// resourceBase adds Configure to a resource.
type resourceBase struct{ base }

func (r *resourceBase) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.configure(req.ProviderData, &resp.Diagnostics)
}

// dataSourceBase adds Configure to a data source.
type dataSourceBase struct{ base }

func (d *dataSourceBase) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.configure(req.ProviderData, &resp.Diagnostics)
}

var (
	uuidRE          = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	domainNameRE    = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	returnPathRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	displayNameRE   = regexp.MustCompile(`^[^"<>\r\n]*$`)
	propertyKeyRE   = regexp.MustCompile(`^[A-Za-z0-9_]{1,50}$`)
	localPartRE     = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)
	trackingLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

func isUUID(s string) bool { return uuidRE.MatchString(s) }

// strValue maps a nullable API string to a Terraform string.
func strValue(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

// optStr maps an API string to a Terraform string, null when empty.
func optStr(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// sameInstant reports whether two timestamps (RFC 3339 or the Postgres text
// form "2006-01-02 15:04:05.999999-07") are the same moment.
func sameInstant(a, b string) bool {
	ta, ea := parseTime(a)
	tb, eb := parseTime(b)
	return ea == nil && eb == nil && ta.Equal(tb)
}

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02T15:04:05.999999999-07"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised time %q", s)
}

// keepTime returns the prior value when it names the same instant as the
// API's, so a timestamp written in another format is not reported as drift.
func keepTime(prior types.String, api *string) types.String {
	if api == nil {
		return types.StringNull()
	}
	if !prior.IsNull() && !prior.IsUnknown() && sameInstant(prior.ValueString(), *api) {
		return prior
	}
	if t, err := parseTime(*api); err == nil {
		return types.StringValue(t.UTC().Format(time.RFC3339))
	}
	return types.StringValue(*api)
}

func apiErr(diags *diag.Diagnostics, action string, err error) {
	diags.AddError("send.io API error", fmt.Sprintf("Unable to %s: %s", action, err))
}
