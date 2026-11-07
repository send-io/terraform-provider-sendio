terraform {
  required_providers {
    sendio = {
      source = "send-io/sendio"
    }
  }
}

# The API key comes from SENDIO_API_KEY when api_key is not set.
provider "sendio" {}
