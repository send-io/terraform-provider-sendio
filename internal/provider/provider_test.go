package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

var testProviders = map[string]func() (tfprotov6.ProviderServer, error){
	"sendio": providerserver.NewProtocol6WithError(New("test")()),
}

// unit runs a test case against the fake API (no TF_ACC needed).
func unit(t *testing.T, f *fakeAPI, steps ...resource.TestStep) {
	t.Helper()
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: testProviders, Steps: steps})
}

func TestUnitDomain(t *testing.T) {
	f := newFakeAPI(t)
	cfg := func(extra string) string {
		return f.providerConfig() + `
resource "sendio_domain" "d" {
  name = "mail.example.com"
` + extra + `
}`
	}
	unit(t, f,
		resource.TestStep{
			Config: cfg(""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("sendio_domain.d", "id"),
				resource.TestCheckResourceAttr("sendio_domain.d", "region", "us-east-1"),
				resource.TestCheckResourceAttr("sendio_domain.d", "custom_return_path", "send"),
				resource.TestCheckResourceAttr("sendio_domain.d", "status", "not_started"),
				resource.TestCheckResourceAttr("sendio_domain.d", "records.#", "3"),
				resource.TestCheckResourceAttr("sendio_domain.d", "records.1.type", "MX"),
				resource.TestCheckResourceAttr("sendio_domain.d", "records.1.priority", "10"),
				resource.TestCheckNoResourceAttr("sendio_domain.d", "tracking_subdomain"),
			),
		},
		resource.TestStep{
			Config: cfg(`  open_tracking = true
  tracking_subdomain = "links"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_domain.d", "open_tracking", "true"),
				resource.TestCheckResourceAttr("sendio_domain.d", "tracking_subdomain", "links"),
				resource.TestCheckResourceAttr("sendio_domain.d", "records.#", "4"),
				func(*terraform.State) error {
					// PATCH carries only what changed.
					patches := f.requestsMatching("PATCH /domains/")
					if len(patches) != 1 || strings.Contains(patches[0], "click_tracking") || !strings.Contains(patches[0], `"tracking_subdomain":"links"`) {
						return fmt.Errorf("unexpected PATCH requests: %v", patches)
					}
					return nil
				},
			),
		},
		resource.TestStep{ResourceName: "sendio_domain.d", ImportState: true, ImportStateVerify: true},
		resource.TestStep{ResourceName: "sendio_domain.d", ImportState: true, ImportStateId: "mail.example.com", ImportStateVerify: true},
		// Removing the tracking subdomain replaces the domain.
		resource.TestStep{
			Config: cfg(`  open_tracking = true`),
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("sendio_domain.d", plancheck.ResourceActionReplace),
			}},
		},
		// Drift: deleted outside Terraform is planned for creation.
		resource.TestStep{
			PreConfig:          func() { f.remove("domains", f.only("domains")) },
			Config:             cfg(`  open_tracking = true`),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
	)
	if f.count("domains") != 0 {
		t.Fatalf("domains left after destroy: %d", f.count("domains"))
	}
}

func TestUnitDomainValidation(t *testing.T) {
	f := newFakeAPI(t)
	for _, tc := range []struct{ body, want string }{
		{`name = "Not A Domain"`, "lower-case domain name"},
		{"name = \"a.example.com\"\n  region = \"mars-1\"", "value must be one of"},
		{"name = \"a.example.com\"\n  tracking_subdomain = \"two.labels\"", "single lower-case DNS label"},
	} {
		unit(t, f, resource.TestStep{
			Config:      f.providerConfig() + "resource \"sendio_domain\" \"d\" {\n  " + tc.body + "\n}",
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(tc.want),
		})
	}
}

func TestUnitDomainVerification(t *testing.T) {
	old := verifyPollInterval
	verifyPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { verifyPollInterval = old })
	f := newFakeAPI(t)
	f.verifyAfter = 3
	unit(t, f, resource.TestStep{
		Config: f.providerConfig() + `
resource "sendio_domain" "d" { name = "mail.example.com" }
resource "sendio_domain_verification" "v" {
  domain_id             = sendio_domain.d.id
  wait_for_verification = true
}`,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("sendio_domain_verification.v", "status", "verified"),
			resource.TestCheckResourceAttr("sendio_domain_verification.v", "verified", "true"),
			func(*terraform.State) error {
				if f.verifyCalls != 3 {
					return fmt.Errorf("verify called %d times, want 3", f.verifyCalls)
				}
				return nil
			},
		),
	})
}

func TestUnitDomainVerificationTimeout(t *testing.T) {
	old := verifyPollInterval
	verifyPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { verifyPollInterval = old })
	f := newFakeAPI(t)
	f.verifyAfter = 0 // never
	unit(t, f, resource.TestStep{
		Config: f.providerConfig() + `
resource "sendio_domain" "d" { name = "mail.example.com" }
resource "sendio_domain_verification" "v" {
  domain_id             = sendio_domain.d.id
  wait_for_verification = true
  timeouts = { create = "1s" }
}`,
		ExpectError: regexp.MustCompile(`Timed out waiting for domain verification`),
	})
}

func TestUnitDomainVerificationTimeoutDuringRequest(t *testing.T) {
	for _, phase := range []string{"verify", "read"} {
		t.Run(phase, func(t *testing.T) {
			f := newFakeAPI(t)
			f.timeoutAt = phase
			unit(t, f, resource.TestStep{
				Config: f.providerConfig() + `
resource "sendio_domain" "d" { name = "mail.example.com" }
resource "sendio_domain_verification" "v" {
  domain_id = sendio_domain.d.id
  wait_for_verification = true
  timeouts = { create = "1s" }
}`,
				ExpectError: regexp.MustCompile(`Timed out waiting for domain verification`),
			})
		})
	}
}

func TestUnitWebhook(t *testing.T) {
	f := newFakeAPI(t)
	unit(t, f,
		resource.TestStep{
			Config: f.providerConfig() + `
resource "sendio_webhook" "w" {
  endpoint = "https://example.com/hook"
  events   = ["email.delivered", "email.bounced"]
  headers  = { "X-Env" = "test" }
  status   = "disabled"
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_webhook.w", "format", "sendio"),
				resource.TestCheckResourceAttr("sendio_webhook.w", "status", "disabled"),
				resource.TestCheckResourceAttr("sendio_webhook.w", "events.#", "2"),
				resource.TestCheckResourceAttr("sendio_webhook.w", "headers.X-Env", "test"),
				resource.TestMatchResourceAttr("sendio_webhook.w", "signing_secret", regexp.MustCompile(`^whsec_`)),
			),
		},
		resource.TestStep{
			Config: f.providerConfig() + `
resource "sendio_webhook" "w" {
  endpoint = "https://example.com/hook2"
  events   = ["email.bounced", "email.delivered", "email.opened"]
  format   = "postmark"
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_webhook.w", "endpoint", "https://example.com/hook2"),
				resource.TestCheckResourceAttr("sendio_webhook.w", "status", "enabled"),
				resource.TestCheckNoResourceAttr("sendio_webhook.w", "headers.%"),
				func(*terraform.State) error {
					w := f.get("webhooks", f.only("webhooks"))
					if h, _ := w["headers"].(map[string]any); len(h) != 0 {
						return fmt.Errorf("headers not cleared: %v", h)
					}
					return nil
				},
			),
		},
		resource.TestStep{ResourceName: "sendio_webhook.w", ImportState: true, ImportStateVerify: true},
		// Drift: a rotated secret and an edited endpoint are picked up.
		resource.TestStep{
			PreConfig: func() {
				f.mutate("webhooks", f.only("webhooks"), func(m map[string]any) { m["endpoint"] = "https://example.com/other" })
			},
			Config: f.providerConfig() + `
resource "sendio_webhook" "w" {
  endpoint = "https://example.com/hook2"
  events   = ["email.bounced", "email.delivered", "email.opened"]
  format   = "postmark"
}`,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
	)
}

func TestUnitWebhookValidation(t *testing.T) {
	f := newFakeAPI(t)
	for _, tc := range []struct{ body, want string }{
		{`endpoint = "http://example.com"` + "\n  events = [\"email.sent\"]", "https:// URL"},
		{`endpoint = "https://example.com"` + "\n  events = [\"email.nope\"]", "value must be one of"},
		{`endpoint = "https://example.com"` + "\n  events = []", "at least 1"},
		{`endpoint = "https://example.com"` + "\n  events = [\"email.sent\"]\n  headers = { Host = \"x\" }", "cannot be set"},
		{`endpoint = "https://example.com"` + "\n  events = [\"email.sent\"]\n  format = \"xml\"", "value must be one of"},
	} {
		unit(t, f, resource.TestStep{
			Config:      f.providerConfig() + "resource \"sendio_webhook\" \"w\" {\n  " + tc.body + "\n}",
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(tc.want),
		})
	}
}

func TestUnitAPIKey(t *testing.T) {
	f := newFakeAPI(t)
	unit(t, f,
		resource.TestStep{
			Config: f.providerConfig() + `resource "sendio_api_key" "k" { name = "ci" }`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_api_key.k", "permission", "full_access"),
				resource.TestMatchResourceAttr("sendio_api_key.k", "token", regexp.MustCompile(`^sio_`)),
				resource.TestCheckResourceAttrSet("sendio_api_key.k", "token_prefix"),
			),
		},
		resource.TestStep{
			Config: f.providerConfig() + `resource "sendio_api_key" "k" { name = "ci-renamed" }`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_api_key.k", "name", "ci-renamed"),
				// The token survives an in-place rename.
				resource.TestMatchResourceAttr("sendio_api_key.k", "token", regexp.MustCompile(`^sio_`)),
			),
		},
		resource.TestStep{
			ResourceName: "sendio_api_key.k", ImportState: true, ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"token"},
		},
		// A key revoked outside Terraform is planned for creation.
		resource.TestStep{
			PreConfig:          func() { f.remove("api-keys", f.only("api-keys")) },
			Config:             f.providerConfig() + `resource "sendio_api_key" "k" { name = "ci-renamed" }`,
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
	)
}

func TestUnitSuppression(t *testing.T) {
	f := newFakeAPI(t)
	unit(t, f,
		resource.TestStep{
			Config: f.providerConfig() + `
resource "sendio_suppression" "s" {
  email      = "Someone@Example.com"
  comment    = "asked to stop"
  expires_at = "2099-01-02T03:04:05Z"
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_suppression.s", "origin", "manual"),
				resource.TestCheckResourceAttr("sendio_suppression.s", "comment", "asked to stop"),
				resource.TestCheckResourceAttr("sendio_suppression.s", "expires_at", "2099-01-02T03:04:05Z"),
			),
		},
		resource.TestStep{
			Config: f.providerConfig() + `
resource "sendio_suppression" "s" {
  email      = "Someone@Example.com"
  expires_at = "2099-01-02T03:04:05Z"
}`,
			Check: resource.TestCheckNoResourceAttr("sendio_suppression.s", "comment"),
		},
		resource.TestStep{
			ResourceName: "sendio_suppression.s", ImportState: true, ImportStateId: "someone@example.com", ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"expires_at"},
		},
	)
}

func TestUnitTemplate(t *testing.T) {
	f := newFakeAPI(t)
	base := f.providerConfig() + `
resource "sendio_template" "t" {
  name    = "Welcome"
  alias   = "welcome"
  subject = "Hi {{{NAME}}}"
  from    = "Acme <hello@mail.example.com>"
  html    = "<p>Hi {{{NAME}}}, you have {{{CREDITS}}} credits</p>"
  publish = true
  variables = [
    { key = "NAME", type = "string", fallback_value = "there" },
    { key = "CREDITS", type = "number", fallback_value = "1.50" },
  ]
}`
	unit(t, f,
		resource.TestStep{
			Config: base,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_template.t", "status", "published"),
				resource.TestCheckResourceAttr("sendio_template.t", "has_unpublished_versions", "false"),
				resource.TestCheckResourceAttr("sendio_template.t", "template_engine", "simple"),
				resource.TestCheckResourceAttr("sendio_template.t", "variables.1.fallback_value", "1.50"),
				resource.TestCheckResourceAttr("sendio_template.t", "version_number", "1"),
				func(*terraform.State) error {
					v := f.get("templates", f.only("templates"))["variables"].([]any)
					if fb := v[1].(map[string]any)["fallback_value"]; fb != 1.5 {
						return fmt.Errorf("number fallback sent as %#v", fb)
					}
					return nil
				},
			),
		},
		resource.TestStep{
			Config: strings.Replace(base, `name    = "Welcome"`, `name    = "Welcome v2"`, 1),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_template.t", "name", "Welcome v2"),
				resource.TestCheckResourceAttr("sendio_template.t", "version_number", "2"),
				resource.TestCheckResourceAttr("sendio_template.t", "status", "published"),
			),
		},
		// The API stores 1.50 as the number 1.5, which is what import reads back.
		resource.TestStep{
			ResourceName: "sendio_template.t", ImportState: true, ImportStateId: "welcome", ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"variables.1.fallback_value"},
		},
		// An unpublished edit made outside Terraform is planned to be published again.
		resource.TestStep{
			PreConfig: func() {
				f.mutate("templates", f.only("templates"), func(m map[string]any) { m["has_unpublished_versions"] = true })
			},
			Config:             strings.Replace(base, `name    = "Welcome"`, `name    = "Welcome v2"`, 1),
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		},
		resource.TestStep{
			Config: strings.Replace(base, `name    = "Welcome"`, `name    = "Welcome v2"`, 1),
			Check:  resource.TestCheckResourceAttr("sendio_template.t", "has_unpublished_versions", "false"),
		},
	)
}

func TestUnitTemplateValidation(t *testing.T) {
	f := newFakeAPI(t)
	for _, tc := range []struct{ body, want string }{
		{`publish = true`, "subject before it can be published"},
		{`variables = [{ key = "FLAG", type = "boolean" }]`, "need template_engine"},
		{`variables = [{ key = "N", type = "number", fallback_value = "abc" }]`, "not a number"},
		{`from = "not an address"`, "not a valid From address"},
	} {
		unit(t, f, resource.TestStep{
			Config:      f.providerConfig() + "resource \"sendio_template\" \"t\" {\n  name = \"x\"\n  html = \"<p>x</p>\"\n  " + tc.body + "\n}",
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(tc.want),
		})
	}
}

func TestUnitMarketing(t *testing.T) {
	f := newFakeAPI(t)
	cfg := func(segName, topicDesc, fallback string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "sendio_segment" "s" { name = %q }
resource "sendio_topic" "t" {
  name                 = "Product news"
  default_subscription = "opt_in"
  %s
}
resource "sendio_contact_property" "p" {
  key  = "seats"
  type = "number"
  %s
}`, segName, topicDesc, fallback)
	}
	unit(t, f,
		resource.TestStep{
			Config: cfg("Customers", `description = "Monthly"`, `fallback_value = "5"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_segment.s", "name", "Customers"),
				resource.TestCheckResourceAttr("sendio_topic.t", "visibility", "private"),
				resource.TestCheckResourceAttr("sendio_topic.t", "description", "Monthly"),
				resource.TestCheckResourceAttr("sendio_contact_property.p", "fallback_value", "5"),
			),
		},
		resource.TestStep{
			Config: cfg("Paying customers", "", ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_segment.s", "name", "Paying customers"),
				resource.TestCheckNoResourceAttr("sendio_topic.t", "description"),
				resource.TestCheckNoResourceAttr("sendio_contact_property.p", "fallback_value"),
			),
		},
		resource.TestStep{ResourceName: "sendio_segment.s", ImportState: true, ImportStateVerify: true},
		resource.TestStep{ResourceName: "sendio_topic.t", ImportState: true, ImportStateVerify: true},
		resource.TestStep{ResourceName: "sendio_contact_property.p", ImportState: true, ImportStateVerify: true},
	)
}

func TestUnitInbox(t *testing.T) {
	f := newFakeAPI(t)
	unit(t, f,
		resource.TestStep{
			Config: f.providerConfig() + `resource "sendio_inbox" "i" { display_name = "Support bot" }`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("sendio_inbox.i", "address", regexp.MustCompile(`^agent-\d+@agents\.example\.test$`)),
				resource.TestCheckResourceAttr("sendio_inbox.i", "domain", "agents.example.test"),
				resource.TestCheckResourceAttrSet("sendio_inbox.i", "username"),
			),
		},
		resource.TestStep{
			Config: f.providerConfig() + `resource "sendio_inbox" "i" { display_name = "Helper" }`,
			Check:  resource.TestCheckResourceAttr("sendio_inbox.i", "display_name", "Helper"),
		},
		resource.TestStep{ResourceName: "sendio_inbox.i", ImportState: true, ImportStateVerify: true},
	)
}

func TestUnitDataSources(t *testing.T) {
	f := newFakeAPI(t)
	unit(t, f, resource.TestStep{
		Config: f.providerConfig() + `
resource "sendio_domain" "d" { name = "mail.example.com" }
resource "sendio_api_key" "k" { name = "ci" }
resource "sendio_template" "t" {
  count = 25
  name  = "t${count.index}"
  html  = "<p>x</p>"
}
data "sendio_domain" "by_name" {
  name       = sendio_domain.d.name
  depends_on = [sendio_domain.d]
}
data "sendio_domains" "all" { depends_on = [sendio_domain.d] }
data "sendio_templates" "all" { depends_on = [sendio_template.t] }
data "sendio_api_keys" "all" { depends_on = [sendio_api_key.k] }
`,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttrPair("data.sendio_domain.by_name", "id", "sendio_domain.d", "id"),
			resource.TestCheckResourceAttr("data.sendio_domain.by_name", "records.#", "3"),
			resource.TestCheckResourceAttr("data.sendio_domains.all", "domains.#", "1"),
			// 25 templates span two pages of the paged list.
			resource.TestCheckResourceAttr("data.sendio_templates.all", "templates.#", "25"),
			resource.TestCheckResourceAttr("data.sendio_api_keys.all", "api_keys.#", "1"),
			resource.TestCheckNoResourceAttr("data.sendio_api_keys.all", "api_keys.0.token"),
		),
	})
}

func TestUnitProviderConfig(t *testing.T) {
	t.Setenv("SENDIO_API_KEY", "")
	f := newFakeAPI(t)
	unit(t, f, resource.TestStep{
		Config:      `resource "sendio_segment" "s" { name = "x" }`,
		ExpectError: regexp.MustCompile(`Missing send.io API key`),
	})
	// The environment variables configure the provider.
	t.Setenv("SENDIO_API_KEY", fakeKey)
	t.Setenv("SENDIO_BASE_URL", f.srv.URL)
	unit(t, f, resource.TestStep{
		Config: `resource "sendio_segment" "s" { name = "from env" }`,
		Check:  resource.TestCheckResourceAttr("sendio_segment.s", "name", "from env"),
	})
	// A wrong key surfaces the API's error.
	unit(t, f, resource.TestStep{
		Config:      `provider "sendio" { api_key = "wrong" }` + "\n" + `resource "sendio_segment" "s" { name = "x" }`,
		ExpectError: regexp.MustCompile(`401 missing_api_key`),
	})
}

// Plan-time validation of attributes the API would reject.
func TestUnitValidation(t *testing.T) {
	f := newFakeAPI(t)
	for _, tc := range []struct{ cfg, want string }{
		{`resource "sendio_api_key" "k" {
  name      = "x"
  domain_id = "00000000-0000-4000-8000-000000000001"
}`, "sending_access"},
		{`resource "sendio_api_key" "k" { name = "` + strings.Repeat("n", 51) + `" }`, "length must be between"},
		{`resource "sendio_suppression" "s" { email = "not-an-address" }`, "not an email address"},
		{`resource "sendio_suppression" "s" {
  email      = "a@example.com"
  expires_at = "tomorrow"
}`, "RFC 3339"},
		{`resource "sendio_topic" "t" {
  name                 = "x"
  default_subscription = "sometimes"
}`, "value must be one of"},
		{`resource "sendio_contact_property" "p" {
  key  = "bad key"
  type = "string"
}`, "letters, digits and underscores"},
		{`resource "sendio_contact_property" "p" {
  key            = "seats"
  type           = "number"
  fallback_value = "many"
}`, "not a number"},
		{`resource "sendio_segment" "s" { name = " padded " }`, "spaces"},
		{`resource "sendio_inbox" "i" { username = "Upper" }`, "lower-case local part"},
		{`resource "sendio_inbox" "i" { display_name = "a <b>" }`, "angle brackets"},
		{`resource "sendio_domain_verification" "v" { domain_id = "x" }`, "domain id"},
		{`data "sendio_domain" "d" {}`, "Exactly one of these attributes"},
	} {
		unit(t, f, resource.TestStep{
			Config:      f.providerConfig() + tc.cfg,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(tc.want),
		})
	}
}
