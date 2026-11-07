resource "sendio_domain" "mail" {
  name = "mail.example.com"
}

# Publish sendio_domain.mail.records first (for example with aws_route53_record
# or cloudflare_record), then wait for the domain to verify.
resource "sendio_domain_verification" "mail" {
  domain_id             = sendio_domain.mail.id
  wait_for_verification = true

  timeouts = {
    create = "45m"
  }
}
