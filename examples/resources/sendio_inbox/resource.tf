# On the shared agent domain: no DNS work needed.
resource "sendio_inbox" "support_agent" {
  username     = "acme-support"
  display_name = "Acme Support"
}

# On your own verified domain.
resource "sendio_inbox" "sales_agent" {
  username     = "sales"
  domain       = "mail.example.com"
  display_name = "Acme Sales"
}
