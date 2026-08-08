package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

func resourceVirtualNetwork() *schema.Resource {
	return &schema.Resource{
		Description:   "An isolated virtual network (OVS bridge, optionally VXLAN-backed).",
		CreateContext: vnetCreate,
		ReadContext:   vnetRead,
		DeleteContext: vnetDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":          {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Network name."},
			"address_space": {Type: schema.TypeString, Required: true, ForceNew: true, Description: "CIDR block, e.g. 10.10.0.0/16."},
			"region":        {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Region to create the network in."},
			"subnet_name":   {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Name of the first subnet."},
			"subnet_cidr":   {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "CIDR of the first subnet."},
			"status":        {Type: schema.TypeString, Computed: true, Description: "Provisioning state."},
		},
	}
}

func vnetCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	regions := c.Regions
	if r, ok := d.GetOk("region"); ok && r.(string) != "" {
		regions = []string{r.(string)}
	}

	payload := map[string]any{
		"name":         d.Get("name").(string),
		"addressSpace": d.Get("address_space").(string),
		"regions":      regions,
		"forwardMode":  "nat",
	}
	if v, ok := d.GetOk("subnet_name"); ok {
		payload["subnetName"] = v.(string)
	}
	if v, ok := d.GetOk("subnet_cidr"); ok {
		payload["subnetCidr"] = v.(string)
	}

	if err := c.Do("POST", "/api/VirtualNetwork/create-vnet", payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(d.Get("name").(string))
	return vnetRead(ctx, d, meta)
}

func vnetRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/api/VirtualNetwork/list-vnets", nil, &resp); err != nil {
		return diag.FromErr(err)
	}
	for _, v := range resp.Data {
		if v.Name == d.Id() {
			_ = d.Set("status", v.Status)
			return nil
		}
	}
	d.SetId("")
	return nil
}

func vnetDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do("DELETE", "/api/VirtualNetwork/delete-vnet", d.Id(), nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

func resourceStorageAccount() *schema.Resource {
	return &schema.Resource{
		Description:   "A storage account.",
		CreateContext: storageCreate,
		ReadContext:   storageRead,
		DeleteContext: storageDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":       {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Storage account name."},
			"region":     {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Primary region."},
			"tier":       {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "standard", Description: "Performance tier."},
			"redundancy": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "lrs", Description: "Redundancy mode."},
			"status":     {Type: schema.TypeString, Computed: true, Description: "Provisioning state."},
		},
	}
}

func storageCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	region := ""
	if r, ok := d.GetOk("region"); ok {
		region = r.(string)
	} else if len(c.Regions) > 0 {
		region = c.Regions[0]
	}

	payload := map[string]any{
		"name":                 d.Get("name").(string),
		"displayName":          d.Get("name").(string),
		"primaryRegion":        region,
		"preferredReadRegion":  region,
		"replicaRegions":       []string{},
		"storageTier":          d.Get("tier").(string),
		"redundancy":           strings.ToUpper(d.Get("redundancy").(string)),
		"consistencyMode":      "session",
		"writeAcknowledgement": "quorum",
	}
	if err := c.Do("POST", "/api/StorageAccount", payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(d.Get("name").(string))
	return storageRead(ctx, d, meta)
}

func storageRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/api/StorageAccount", nil, &resp); err != nil {
		return diag.FromErr(err)
	}
	for _, s := range resp.Data {
		if s.Name == d.Id() {
			_ = d.Set("status", s.Status)
			return nil
		}
	}
	d.SetId("")
	return nil
}

func storageDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do("DELETE", "/api/StorageAccount/"+d.Id(), nil, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
