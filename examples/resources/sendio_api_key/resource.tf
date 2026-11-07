resource "sendio_domain" "mail" {
  name = "mail.example.com"
}

# A key that can only send from mail.example.com.
resource "sendio_api_key" "app" {
  name       = "app-production"
  permission = "sending_access"
  domain_id  = sendio_domain.mail.id
}

output "app_api_key" {
  value     = sendio_api_key.app.token
  sensitive = true
}
