data "sendio_templates" "all" {}

output "published_templates" {
  value = [for t in data.sendio_templates.all.templates : t.alias if t.status == "published"]
}
