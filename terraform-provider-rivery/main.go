// terraform-provider-rivery is the Terraform provider for Boomi Data
// Integration (Rivery). It lets teams declare environments, connections and
// data flows in .tf and reconcile them through the Data Integration API.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/boomi/terraform-provider-rivery/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// version is overridden at build time via -ldflags (see GoReleaser / Makefile).
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		// Registry address: namespace/type → boomi/rivery.
		Address: "registry.terraform.io/boomi/rivery",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
