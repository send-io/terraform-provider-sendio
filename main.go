package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/send-io/terraform-provider-sendio/internal/provider"
)

// Generate the registry docs (docs/) from the schema and examples/.
//go:generate go tool tfplugindocs generate --provider-name sendio

// version is set by goreleaser (-X main.version=...).
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		// The registry address users put in required_providers. Placeholder
		// until the namespace is claimed on the Terraform Registry.
		Address: "registry.terraform.io/send-io/sendio",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
