resource "sendio_topic" "product_news" {
  name                 = "Product news"
  description          = "New features, once a month"
  default_subscription = "opt_in"
  visibility           = "public"
}
