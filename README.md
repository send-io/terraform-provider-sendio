# send.io Terraform provider

Manage send.io as code: sending domains and their verification, webhooks, API keys,
templates, segments, topics, contact properties, suppressions and agent inboxes.

Built on the HashiCorp [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework)
(protocol 6, Terraform 1.0 or later). It calls the send.io REST API directly.

## Use it

```hcl
terraform {
  required_providers {
    sendio = {
      source = "send-io/sendio"
    }
  }
}

provider "sendio" {} # reads SENDIO_API_KEY

resource "sendio_domain" "mail" {
  name           = "mail.example.com"
  click_tracking = true
}

resource "sendio_domain_verification" "mail" {
  domain_id             = sendio_domain.mail.id
  wait_for_verification = true
}

resource "sendio_webhook" "events" {
  endpoint = "https://example.com/webhooks/sendio"
  events   = ["email.delivered", "email.bounced"]
}
```

The provider needs a **full access** API key.

| Setting    | Environment variable | Default               |
|------------|----------------------|-----------------------|
| `api_key`  | `SENDIO_API_KEY`     | none (required)       |
| `base_url` | `SENDIO_BASE_URL`    | `https://api.send.io` |

## Resources and data sources

| Resource                     | Notes |
|------------------------------|-------|
| `sendio_domain`              | `name`, `region`, `custom_return_path` replace; tracking settings update in place; `records` lists the DNS records to publish. |
| `sendio_domain_verification` | Runs a DNS check, and with `wait_for_verification` waits (default timeout 30m). Destroy is a no-op. |
| `sendio_webhook`             | `signing_secret` is sensitive; a rotation outside Terraform shows on refresh. |
| `sendio_api_key`             | `token` is only returned on create (sensitive, in state). Destroy revokes. Imported keys have no token. |
| `sendio_suppression`         | `comment` updates in place; `email`, `origin`, `expires_at` replace. Import by id or address. |
| `sendio_template`            | Each change saves a new draft version; `publish = true` publishes it. Import by id or alias. |
| `sendio_segment`             | Rename in place. |
| `sendio_topic`               | `default_subscription` replaces (the API cannot change it). |
| `sendio_contact_property`    | `key` and `type` replace; `fallback_value` updates. |
| `sendio_inbox`               | Agent inbox; only `display_name` updates in place. Import by id or address. |

Data sources: `sendio_domain` (by id or name), `sendio_domains`, `sendio_templates`,
`sendio_api_keys` (never returns tokens).

Every resource supports `terraform import`, refreshes from the API on every plan
(objects deleted outside Terraform are planned for creation again) and validates
values at plan time where the API's rules are known. Full reference: [`docs/`](docs/),
examples: [`examples/`](examples/).

## Develop

Go 1.25 or later.

```sh
go build ./...
go test ./...                     # unit tests: real Terraform against an in-memory fake API
go generate ./...                 # regenerate docs/ with tfplugindocs (go tool)
```

Unit tests need a `terraform` binary on `PATH` (or `TF_ACC_TERRAFORM_PATH`).

### Acceptance tests

These create and destroy real objects (names start with `tf-acc-`). Use a separate test team, never one in real use.

```sh
TF_ACC=1 \
SENDIO_API_KEY=... \
SENDIO_BASE_URL=https://api.send.io \
SENDIO_ACC_DOMAIN=<a verified domain of that team> \
go test ./internal/provider -run TestAcc -v
```

`TestAccDomain` adds and removes a throwaway subdomain of `SENDIO_ACC_DOMAIN`; templates
publish with a From address on it.

### Try a local build with the Terraform CLI

```sh
go build -o "$HOME/.terraform.d/dev/terraform-provider-sendio" .
cat > dev.tfrc <<EOF
provider_installation {
  dev_overrides { "send-io/sendio" = "$HOME/.terraform.d/dev" }
  direct {}
}
EOF
TF_CLI_CONFIG_FILE=$PWD/dev.tfrc terraform plan   # no terraform init needed for the override
```

## Release

`.goreleaser.yml` builds the Registry layout (one zip per OS and architecture,
`SHA256SUMS`, its detached GPG signature, and `terraform-registry-manifest.json`).
Before the first release:

- the provider needs its own repository named `terraform-provider-sendio` (the Registry
  publishes from a GitHub repository with that name and `v*` tags), and the module path
  here (`github.com/send-io/terraform-provider-sendio`) updated to match;
- create the release GPG key, add the public key to the Registry namespace and set
  `GPG_FINGERPRINT` for goreleaser (marked TODO in the config).

Nothing is published from this directory today.
