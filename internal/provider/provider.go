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
				DefaultFunc: schema.EnvDefaultFunc("HIOK_ENDPOINT", "http://localhost:5001"),
				Description: "Base URL of the HIOK API, e.g. https://hiokcloud.com.",
			},
			"token": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_TOKEN", nil),
				Description: "API token. Takes precedence over email/password.",
			},
			"email": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_EMAIL", nil),
				Description: "Account email, used to obtain a token when none is set.",
			},
			"password": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_PASSWORD", nil),
				Description: "Account password, used to obtain a token when none is set.",
			},
			"regions": {
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Default regions for resources that do not set their own.",
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
	if len(regions) == 0 {
		regions = []string{"south-india"}
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
