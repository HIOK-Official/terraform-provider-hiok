// Command terraform-provider-hiok is the Terraform provider for HIOK Cloud.
package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/provider"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{ProviderFunc: provider.New})
}
