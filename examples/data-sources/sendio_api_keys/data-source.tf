data "sendio_api_keys" "all" {}

output "api_key_names" {
  value = data.sendio_api_keys.all.api_keys[*].name
}
