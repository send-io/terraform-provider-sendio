resource "sendio_domain" "mail" {
  name               = "mail.example.com"
  region             = "us-east-1"
  open_tracking      = true
  click_tracking     = true
  tracking_subdomain = "links"
}

# Publish the records with your DNS provider, for example:
output "dns_records" {
  value = sendio_domain.mail.records
}
