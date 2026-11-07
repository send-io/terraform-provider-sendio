data "sendio_domain" "mail" {
  name = "mail.example.com"
}

output "mail_domain_status" {
  value = data.sendio_domain.mail.status
}
