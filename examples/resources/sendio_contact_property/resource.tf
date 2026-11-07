resource "sendio_contact_property" "plan" {
  key            = "plan"
  type           = "string"
  fallback_value = "free"
}

resource "sendio_contact_property" "seats" {
  key  = "seats"
  type = "number"
}
