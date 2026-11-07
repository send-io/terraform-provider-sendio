resource "sendio_webhook" "events" {
  endpoint = "https://example.com/webhooks/sendio"
  events   = ["email.delivered", "email.bounced", "email.complained"]
  format   = "sendio"
  headers = {
    "X-Environment" = "production"
  }
}

output "webhook_signing_secret" {
  value     = sendio_webhook.events.signing_secret
  sensitive = true
}
