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
			"ids":           {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "All region IDs, usable as `region` on resources."},
			"names":         {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Display names, in the same order as `ids`."},
			"available_ids": {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "IDs of the regions currently accepting new resources."},
		},
	}
}

func readRegions(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	regions, err := c.ListRegions(ctx)
	if err != nil {
		return diag.FromErr(err)
	}
	ids := make([]string, 0, len(regions))
	names := make([]string, 0, len(regions))
	available := make([]string, 0, len(regions))
	for _, r := range regions {
		ids = append(ids, r.ID)
		names = append(names, r.DisplayName)
		if r.IsAvailable {
			available = append(available, r.ID)
		}
	}
	_ = d.Set("ids", ids)
	_ = d.Set("names", names)
	_ = d.Set("available_ids", available)
	d.SetId("regions")
	return nil
}

func dataSourceVmImages() *schema.Resource {
	return &schema.Resource{
		Description: "Base images available for virtual machines.",
		ReadContext: readVmImages,
		Schema: map[string]*schema.Schema{
			"ids":           {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Image IDs, usable as `image` on `hiok_virtual_machine` (e.g. `ubuntu-24.04-amd64`)."},
			"architectures": {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "CPU architecture of each image (`amd64`, `arm64`), in the same order as `ids`."},
			"names":         {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Image names, in the same order as `ids`."},
			"descriptions":  {Type: schema.TypeList, Computed: true, Elem: &schema.Schema{Type: schema.TypeString}, Description: "Descriptions, in the same order as `ids`."},
		},
	}
}

func readVmImages(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			DocumentId   string `json:"documentId"`
			ImageName    string `json:"imageName"`
			Description  string `json:"description"`
			Architecture string `json:"architecture"`
		} `json:"data"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/VirtualMachine/list-local-vm-images", nil, &resp); err != nil {
		return diag.FromErr(err)
	}

	ids := make([]string, 0, len(resp.Data))
	names := make([]string, 0, len(resp.Data))
	descriptions := make([]string, 0, len(resp.Data))
	archs := make([]string, 0, len(resp.Data))
	for _, i := range resp.Data {
		archs = append(archs, i.Architecture)
		ids = append(ids, i.DocumentId)
		names = append(names, i.ImageName)
		descriptions = append(descriptions, i.Description)
	}
	_ = d.Set("ids", ids)
	_ = d.Set("names", names)
	_ = d.Set("descriptions", descriptions)
	_ = d.Set("architectures", archs)
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
			"vm_id":      {Type: schema.TypeString, Computed: true},
			"vcpu_count": {Type: schema.TypeInt, Computed: true},
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
	_ = d.Set("status", vm.state())
	_ = d.Set("region", vm.RegionId)
	_ = d.Set("private_ip", vm.PrivateIp)
	_ = d.Set("vm_id", vm.ID)
	_ = d.Set("vcpu_count", vm.VCPU)
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
			"vnet_id":       {Type: schema.TypeString, Computed: true},
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
	_ = d.Set("address_space", v.ipv4Space())
	_ = d.Set("vnet_id", v.ID)
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
			"account_id":     {Type: schema.TypeString, Computed: true},
			"tier":           {Type: schema.TypeString, Computed: true},
			"redundancy":     {Type: schema.TypeString, Computed: true},
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
	_ = d.Set("account_id", s.ID)
	_ = d.Set("tier", s.StorageTier)
	_ = d.Set("redundancy", s.Redundancy)
	d.SetId(want)
	return nil
}

func dataSourceContainer() *schema.Resource {
	return &schema.Resource{
		Description: "Look up an existing container by name.",
		ReadContext: readContainerData,
		Schema: map[string]*schema.Schema{
			"name":         {Type: schema.TypeString, Required: true, ValidateFunc: validateName},
			"status":       {Type: schema.TypeString, Computed: true},
			"image":        {Type: schema.TypeString, Computed: true},
			"dns_hostname": {Type: schema.TypeString, Computed: true},
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
	_ = d.Set("dns_hostname", ct.DNSHostname)
	d.SetId(want)
	return nil
}
