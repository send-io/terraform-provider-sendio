package provider

// Acceptance tests: real API calls. Run with
//
//	TF_ACC=1 SENDIO_API_KEY=... SENDIO_BASE_URL=https://api.sendio.run go test ./internal/provider -run TestAcc -v
//
// Every object is named tf-acc-<random> and removed by destroy; CheckDestroy
// confirms the API answers 404 afterwards. SENDIO_ACC_DOMAIN is a verified
// sending domain of the key's team (templates publish with a From on it and
// the throwaway domain is created as a subdomain of it).

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/send-io/terraform-provider-sendio/internal/client"
)

func accPreCheck(t *testing.T) {
	for _, k := range []string{"SENDIO_API_KEY", "SENDIO_BASE_URL", "SENDIO_ACC_DOMAIN"} {
		if os.Getenv(k) == "" {
			t.Fatalf("%s must be set for acceptance tests", k)
		}
	}
}

func accClient() *client.Client {
	return client.New(os.Getenv("SENDIO_BASE_URL"), os.Getenv("SENDIO_API_KEY"), "terraform-provider-sendio/acc")
}

// gone checks that every resource of type typ is 404 at base/<id>.
func gone(typ, base string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		c := accClient()
		for _, rs := range s.RootModule().Resources {
			if rs.Type != typ {
				continue
			}
			err := c.Get(context.Background(), client.Path(base, rs.Primary.ID), nil)
			if err == nil {
				return fmt.Errorf("%s %s still exists after destroy", typ, rs.Primary.ID)
			}
			if !client.IsNotFound(err) {
				return fmt.Errorf("%s %s: %v", typ, rs.Primary.ID, err)
			}
		}
		return nil
	}
}

func acc(t *testing.T, destroy resource.TestCheckFunc, steps ...resource.TestStep) {
	t.Helper()
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: testProviders,
		CheckDestroy:             destroy,
		Steps:                    steps,
	})
}

func rnd() string { return "tf-acc-" + acctest.RandString(8) }

func TestAccDomain(t *testing.T) {
	name := rnd() + "." + os.Getenv("SENDIO_ACC_DOMAIN")
	cfg := func(extra string) string {
		return fmt.Sprintf("resource \"sendio_domain\" \"d\" {\n  name = %q\n%s\n}\n", name, extra)
	}
	acc(t, gone("sendio_domain", "/domains"),
		resource.TestStep{
			Config: cfg(""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_domain.d", "name", name),
				resource.TestCheckResourceAttr("sendio_domain.d", "region", "us-east-1"),
				resource.TestCheckResourceAttrSet("sendio_domain.d", "status"),
				resource.TestMatchResourceAttr("sendio_domain.d", "records.#", regexp.MustCompile(`^[1-9]`)),
			),
		},
		resource.TestStep{
			Config: cfg("  open_tracking = true\n  click_tracking = true"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_domain.d", "open_tracking", "true"),
				resource.TestCheckResourceAttr("sendio_domain.d", "click_tracking", "true"),
			),
		},
		resource.TestStep{ResourceName: "sendio_domain.d", ImportState: true, ImportStateVerify: true,
			// Record statuses and the domain status move on their own while DNS is checked.
			ImportStateVerifyIgnore: []string{"status", "records", "tracking_status"}},
		resource.TestStep{
			Config: cfg("  open_tracking = true\n  click_tracking = true") + `
resource "sendio_domain_verification" "v" { domain_id = sendio_domain.d.id }
data "sendio_domain" "byname" {
  name       = sendio_domain.d.name
  depends_on = [sendio_domain.d]
}
data "sendio_domains" "all" { depends_on = [sendio_domain.d] }`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("sendio_domain_verification.v", "status"),
				resource.TestCheckResourceAttrPair("data.sendio_domain.byname", "id", "sendio_domain.d", "id"),
				resource.TestMatchResourceAttr("data.sendio_domains.all", "domains.#", regexp.MustCompile(`^[1-9]`)),
			),
		},
	)
}

func TestAccWebhook(t *testing.T) {
	ep := "https://example.com/" + rnd()
	cfg := func(extra string) string {
		return fmt.Sprintf(`resource "sendio_webhook" "w" {
  endpoint = %q
  events   = ["email.delivered", "email.bounced"]
%s
}`, ep, extra)
	}
	acc(t, gone("sendio_webhook", "/webhooks"),
		resource.TestStep{
			Config: cfg(`  headers = { "X-Tf" = "1" }`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_webhook.w", "format", "sendio"),
				resource.TestCheckResourceAttr("sendio_webhook.w", "status", "enabled"),
				resource.TestCheckResourceAttr("sendio_webhook.w", "headers.X-Tf", "1"),
				resource.TestMatchResourceAttr("sendio_webhook.w", "signing_secret", regexp.MustCompile(`^whsec_`)),
			),
		},
		resource.TestStep{
			Config: cfg("  status = \"disabled\"\n  format = \"postmark\""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_webhook.w", "status", "disabled"),
				resource.TestCheckResourceAttr("sendio_webhook.w", "format", "postmark"),
				resource.TestCheckNoResourceAttr("sendio_webhook.w", "headers.%"),
			),
		},
		resource.TestStep{ResourceName: "sendio_webhook.w", ImportState: true, ImportStateVerify: true},
	)
}

func TestAccTemplate(t *testing.T) {
	alias := rnd()
	from := "Ada <ada@" + os.Getenv("SENDIO_ACC_DOMAIN") + ">"
	cfg := func(name string) string {
		return fmt.Sprintf(`resource "sendio_template" "t" {
  name    = %q
  alias   = %q
  subject = "Hello {{{NAME}}}"
  from    = %q
  html    = "<p>Hello {{{NAME}}}, {{{SEATS}}} seats</p>"
  text    = "Hello {{{NAME}}}"
  publish = true
  variables = [
    { key = "NAME", type = "string", fallback_value = "there" },
    { key = "SEATS", type = "number", fallback_value = "3" },
  ]
}
data "sendio_templates" "all" { depends_on = [sendio_template.t] }`, name, alias, from)
	}
	acc(t, gone("sendio_template", "/templates"),
		resource.TestStep{
			Config: cfg(alias),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_template.t", "status", "published"),
				resource.TestCheckResourceAttr("sendio_template.t", "has_unpublished_versions", "false"),
				resource.TestCheckResourceAttr("sendio_template.t", "variables.0.fallback_value", "there"),
				resource.TestCheckResourceAttr("sendio_template.t", "variables.1.fallback_value", "3"),
				resource.TestMatchResourceAttr("data.sendio_templates.all", "templates.#", regexp.MustCompile(`^[1-9]`)),
			),
		},
		resource.TestStep{
			Config: cfg(alias + " v2"),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_template.t", "name", alias+" v2"),
				resource.TestCheckResourceAttr("sendio_template.t", "status", "published"),
				resource.TestCheckResourceAttr("sendio_template.t", "has_unpublished_versions", "false"),
			),
		},
		resource.TestStep{ResourceName: "sendio_template.t", ImportState: true, ImportStateId: alias, ImportStateVerify: true},
	)
}

func TestAccMarketing(t *testing.T) {
	n := rnd()
	key := "tf_acc_" + acctest.RandString(8)
	cfg := func(seg, desc, fb string) string {
		return fmt.Sprintf(`resource "sendio_segment" "s" { name = %q }
resource "sendio_topic" "t" {
  name                 = %q
  default_subscription = "opt_out"
  visibility           = "public"
  %s
}
resource "sendio_contact_property" "p" {
  key  = %q
  type = "string"
  %s
}`, seg, n, desc, key, fb)
	}
	acc(t, resource.ComposeAggregateTestCheckFunc(
		gone("sendio_segment", "/segments"), gone("sendio_topic", "/topics"), gone("sendio_contact_property", "/contact-properties")),
		resource.TestStep{
			Config: cfg(n, `description = "From Terraform"`, `fallback_value = "none"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_segment.s", "name", n),
				resource.TestCheckResourceAttr("sendio_topic.t", "description", "From Terraform"),
				resource.TestCheckResourceAttr("sendio_topic.t", "visibility", "public"),
				resource.TestCheckResourceAttr("sendio_contact_property.p", "fallback_value", "none"),
			),
		},
		resource.TestStep{
			Config: cfg(n+"-renamed", "", ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_segment.s", "name", n+"-renamed"),
				resource.TestCheckNoResourceAttr("sendio_topic.t", "description"),
				resource.TestCheckNoResourceAttr("sendio_contact_property.p", "fallback_value"),
			),
		},
		resource.TestStep{ResourceName: "sendio_segment.s", ImportState: true, ImportStateVerify: true},
		resource.TestStep{ResourceName: "sendio_topic.t", ImportState: true, ImportStateVerify: true},
		resource.TestStep{ResourceName: "sendio_contact_property.p", ImportState: true, ImportStateVerify: true},
	)
}

func TestAccSuppression(t *testing.T) {
	email := rnd() + "@example.com"
	cfg := func(comment string) string {
		return fmt.Sprintf("resource \"sendio_suppression\" \"s\" {\n  email = %q\n  %s\n}", email, comment)
	}
	acc(t, gone("sendio_suppression", "/suppressions"),
		resource.TestStep{
			Config: cfg(`comment = "terraform acceptance test"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_suppression.s", "origin", "manual"),
				resource.TestCheckResourceAttr("sendio_suppression.s", "comment", "terraform acceptance test"),
			),
		},
		resource.TestStep{
			Config: cfg(""),
			Check:  resource.TestCheckNoResourceAttr("sendio_suppression.s", "comment"),
		},
		resource.TestStep{ResourceName: "sendio_suppression.s", ImportState: true, ImportStateId: email, ImportStateVerify: true},
	)
}

func TestAccAPIKey(t *testing.T) {
	n := rnd()
	acc(t, gone("sendio_api_key", "/api-keys"),
		resource.TestStep{
			Config: fmt.Sprintf(`resource "sendio_api_key" "k" {
  name       = %q
  permission = "sending_access"
}
data "sendio_api_keys" "all" { depends_on = [sendio_api_key.k] }`, n),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_api_key.k", "permission", "sending_access"),
				resource.TestMatchResourceAttr("sendio_api_key.k", "token", regexp.MustCompile(`^\S{20,}$`)),
				resource.TestMatchResourceAttr("data.sendio_api_keys.all", "api_keys.#", regexp.MustCompile(`^[1-9]`)),
			),
		},
		resource.TestStep{
			Config: fmt.Sprintf(`resource "sendio_api_key" "k" {
  name       = "%s-renamed"
  permission = "sending_access"
}`, n),
			Check: resource.TestCheckResourceAttr("sendio_api_key.k", "name", n+"-renamed"),
		},
		resource.TestStep{ResourceName: "sendio_api_key.k", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"token"}},
	)
}

func TestAccInbox(t *testing.T) {
	user := rnd()
	acc(t, gone("sendio_inbox", "/inboxes"),
		resource.TestStep{
			Config: fmt.Sprintf(`resource "sendio_inbox" "i" {
  username     = %q
  display_name = "Terraform test"
}`, user),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("sendio_inbox.i", "username", user),
				resource.TestCheckResourceAttrSet("sendio_inbox.i", "domain"),
			),
		},
		resource.TestStep{
			Config: fmt.Sprintf(`resource "sendio_inbox" "i" {
  username     = %q
  display_name = "Terraform test 2"
}`, user),
			Check: resource.TestCheckResourceAttr("sendio_inbox.i", "display_name", "Terraform test 2"),
		},
		resource.TestStep{ResourceName: "sendio_inbox.i", ImportState: true, ImportStateVerify: true},
	)
}
