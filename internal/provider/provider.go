package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// New returns the provider schema and wiring.
func New() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"endpoint": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_ENDPOINT", nil),
				Description: "Base URL of the HIOK API, e.g. https://hiokcloud.com. Can be set with HIOK_ENDPOINT.",
			},
			"token": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_TOKEN", nil),
				Description: "API token. Takes precedence over email/password. Can be set with HIOK_TOKEN.",
			},
			"email": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_EMAIL", nil),
				Description: "Account email, used to obtain a token when none is set. Can be set with HIOK_EMAIL.",
			},
			"password": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_PASSWORD", nil),
				Description: "Account password, used to obtain a token when none is set. Can be set with HIOK_PASSWORD.",
			},
			"regions": {
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Default region for resources that do not set their own (the first entry is used). Defaults to the first available region reported by the API, e.g. \"canada\".",
			},
		},

		ResourcesMap: map[string]*schema.Resource{
			"hiok_virtual_machine": resourceVirtualMachine(),
			"hiok_container":       resourceContainer(),
			"hiok_virtual_network": resourceVirtualNetwork(),
			"hiok_storage_account": resourceStorageAccount(),
		},

		DataSourcesMap: map[string]*schema.Resource{
			"hiok_regions":         dataSourceRegions(),
			"hiok_vm_images":       dataSourceVmImages(),
			"hiok_virtual_machine": dataSourceVirtualMachine(),
			"hiok_virtual_network": dataSourceVirtualNetwork(),
			"hiok_storage_account": dataSourceStorageAccount(),
			"hiok_container":       dataSourceContainer(),
		},

		ConfigureContextFunc: configure,
	}
}

func configure(_ context.Context, d *schema.ResourceData) (any, diag.Diagnostics) {
	regions := make([]string, 0)
	for _, r := range d.Get("regions").([]any) {
		if s, ok := r.(string); ok && s != "" {
			regions = append(regions, s)
		}
	}

	c, err := client.New(
		d.Get("endpoint").(string),
		d.Get("token").(string),
		d.Get("email").(string),
		d.Get("password").(string),
		regions,
	)
	if err != nil {
		return nil, diag.FromErr(err)
	}
	return c, nil
}
