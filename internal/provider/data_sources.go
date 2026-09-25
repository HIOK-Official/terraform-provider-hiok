package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

func dataSourceRegions() *schema.Resource {
	return &schema.Resource{
		Description: "Regions this deployment can provision into.",
		ReadContext: readRegions,
		Schema: map[string]*schema.Schema{
			"ids":   {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Region IDs, usable as `region` on resources."},
			"names": {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Display names, in the same order as `ids`."},
		},
	}
}

func readRegions(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			Id          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"data"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/storageaccount/regions", nil, &resp); err != nil {
		return diag.FromErr(err)
	}

	ids := make([]string, 0, len(resp.Data))
	names := make([]string, 0, len(resp.Data))
	for _, r := range resp.Data {
		ids = append(ids, r.Id)
		names = append(names, r.DisplayName)
	}
	_ = d.Set("ids", ids)
	_ = d.Set("names", names)
	d.SetId("regions")
	return nil
}

func dataSourceVmImages() *schema.Resource {
	return &schema.Resource{
		Description: "Base images available for virtual machines.",
		ReadContext: readVmImages,
		Schema: map[string]*schema.Schema{
			"ids":          {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Image document IDs."},
			"names":        {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Image names, in the same order as `ids`."},
			"descriptions": {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Descriptions, in the same order as `ids`."},
		},
	}
}

func readVmImages(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			DocumentId  string `json:"documentId"`
			ImageName   string `json:"imageName"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/VirtualMachine/list-local-vm-images", nil, &resp); err != nil {
		return diag.FromErr(err)
	}

	ids := make([]string, 0, len(resp.Data))
	names := make([]string, 0, len(resp.Data))
	descriptions := make([]string, 0, len(resp.Data))
	for _, i := range resp.Data {
		ids = append(ids, i.DocumentId)
		names = append(names, i.ImageName)
		descriptions = append(descriptions, i.Description)
	}
	_ = d.Set("ids", ids)
	_ = d.Set("names", names)
	_ = d.Set("descriptions", descriptions)
	d.SetId("vm-images")
	return nil
}

func dataSourceVirtualMachine() *schema.Resource {
	return &schema.Resource{
		Description: "Look up an existing virtual machine by name.",
		ReadContext: readVirtualMachine,
		Schema: map[string]*schema.Schema{
			"name":       {Type: schema.TypeString, Required: true, ValidateFunc: validateName},
			"status":     {Type: schema.TypeString, Computed: true},
			"region":     {Type: schema.TypeString, Computed: true},
			"private_ip": {Type: schema.TypeString, Computed: true},
		},
	}
}

func readVirtualMachine(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	want := d.Get("name").(string)
	vm, err := findVM(ctx, c, want)
	if err != nil {
		return diag.FromErr(err)
	}
	if vm == nil {
		return diag.Errorf("no virtual machine named %q", want)
	}
	_ = d.Set("status", vm.Status)
	_ = d.Set("region", vm.RegionId)
	_ = d.Set("private_ip", vm.PrivateIp)
	d.SetId(want)
	return nil
}

func dataSourceVirtualNetwork() *schema.Resource {
	return &schema.Resource{
		Description: "Look up an existing virtual network by name.",
		ReadContext: readVirtualNetwork,
		Schema: map[string]*schema.Schema{
			"name":          {Type: schema.TypeString, Required: true, ValidateFunc: validateName},
			"status":        {Type: schema.TypeString, Computed: true},
			"address_space": {Type: schema.TypeString, Computed: true},
		},
	}
}

func readVirtualNetwork(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	want := d.Get("name").(string)
	v, err := findVnet(ctx, c, want)
	if err != nil {
		return diag.FromErr(err)
	}
	if v == nil {
		return diag.Errorf("no virtual network named %q", want)
	}
	_ = d.Set("status", v.Status)
	_ = d.Set("address_space", v.AddressSpace)
	d.SetId(want)
	return nil
}

func dataSourceStorageAccount() *schema.Resource {
	return &schema.Resource{
		Description: "Look up an existing storage account by name.",
		ReadContext: readStorageAccount,
		Schema: map[string]*schema.Schema{
			"name":           {Type: schema.TypeString, Required: true, ValidateFunc: validateName},
			"status":         {Type: schema.TypeString, Computed: true},
			"primary_region": {Type: schema.TypeString, Computed: true},
		},
	}
}

func readStorageAccount(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	want := d.Get("name").(string)
	s, err := findStorage(ctx, c, want)
	if err != nil {
		return diag.FromErr(err)
	}
	if s == nil {
		return diag.Errorf("no storage account named %q", want)
	}
	_ = d.Set("status", s.Status)
	_ = d.Set("primary_region", s.PrimaryRegion)
	d.SetId(want)
	return nil
}

func dataSourceContainer() *schema.Resource {
	return &schema.Resource{
		Description: "Look up an existing container by name.",
		ReadContext: readContainerData,
		Schema: map[string]*schema.Schema{
			"name":   {Type: schema.TypeString, Required: true, ValidateFunc: validateName},
			"status": {Type: schema.TypeString, Computed: true},
			"image":  {Type: schema.TypeString, Computed: true},
		},
	}
}

func readContainerData(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	want := d.Get("name").(string)
	ct, err := findContainer(ctx, c, want)
	if err != nil {
		return diag.FromErr(err)
	}
	if ct == nil {
		return diag.Errorf("no container named %q", want)
	}
	_ = d.Set("status", ct.Status)
	_ = d.Set("image", ct.Image)
	d.SetId(want)
	return nil
}
