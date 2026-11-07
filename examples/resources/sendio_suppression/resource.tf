resource "sendio_suppression" "legal_hold" {
  email   = "someone@example.com"
  comment = "Asked by legal not to email"
}
