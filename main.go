// Command terraform-provider-hiok is the Terraform provider for HIOK Cloud.
package main

import (
	"flag"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/provider"
)

// Set by goreleaser.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	var debug bool
	var showVersion bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.BoolVar(&showVersion, "version", false, "print the version and exit")
	flag.Parse()
	if showVersion {
		fmt.Printf("terraform-provider-hiok %s (%s)\n", version, commit)
		return
	}

	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: provider.New,
		ProviderAddr: "registry.terraform.io/HIOK-Official/hiok",
		Debug:        debug,
	})
}
