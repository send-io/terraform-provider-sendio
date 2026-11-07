data "sendio_domains" "all" {}

output "verified_domains" {
  value = [for d in data.sendio_domains.all.domains : d.name if d.status == "verified"]
}
