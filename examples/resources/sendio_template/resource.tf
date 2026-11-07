resource "sendio_template" "welcome" {
  name    = "Welcome"
  alias   = "welcome"
  subject = "Welcome, {{{FIRST_NAME}}}"
  from    = "Acme <hello@mail.example.com>"
  html    = "<p>Hi {{{FIRST_NAME}}}, your trial has {{{DAYS}}} days left.</p>"
  text    = "Hi {{{FIRST_NAME}}}, your trial has {{{DAYS}}} days left."
  publish = true

  variables = [
    { key = "FIRST_NAME", type = "string", fallback_value = "there" },
    { key = "DAYS", type = "number", fallback_value = "14" },
  ]
}
