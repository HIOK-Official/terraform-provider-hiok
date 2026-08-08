package provider

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

func dataSourceRegions() *schema.Resource {
	return &schema.Resource{
		Description: "Regions this deployment can provision into.",
		ReadContext: readRegions,
		Schema: map[string]*schema.Schema{
			"ids": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"names": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
		},
	}
}

func readRegions(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			Id          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/api/storageaccount/regions", nil, &resp); err != nil {
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
	d.SetId("regions-" + strconv.FormatInt(time.Now().Unix(), 10))
	return nil
}

func dataSourceVmImages() *schema.Resource {
	return &schema.Resource{
		Description: "Base images available for virtual machines.",
		ReadContext: readVmImages,
		Schema: map[string]*schema.Schema{
			"ids": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"descriptions": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
		},
	}
}

func readVmImages(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			DocumentId  string `json:"documentId"`
			ImageName   string `json:"imageName"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/api/VirtualMachine/list-local-vm-images", nil, &resp); err != nil {
		return diag.FromErr(err)
	}

	ids := make([]string, 0, len(resp.Data))
	descriptions := make([]string, 0, len(resp.Data))
	for _, i := range resp.Data {
		ids = append(ids, i.DocumentId)
		descriptions = append(descriptions, i.Description)
	}
	_ = d.Set("ids", ids)
	_ = d.Set("descriptions", descriptions)
	d.SetId("vm-images-" + strconv.FormatInt(time.Now().Unix(), 10))
	return nil
}

func dataSourceVirtualMachine() *schema.Resource {
	return &schema.Resource{
		Description: "Look up an existing virtual machine by name.",
		ReadContext: readVirtualMachine,
		Schema: map[string]*schema.Schema{
			"name":       {Type: schema.TypeString, Required: true},
			"status":     {Type: schema.TypeString, Computed: true},
			"region":     {Type: schema.TypeString, Computed: true},
			"private_ip": {Type: schema.TypeString, Computed: true},
		},
	}
}

func readVirtualMachine(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			VMName    string `json:"vmName"`
			Status    string `json:"status"`
			RegionId  string `json:"regionId"`
			PrivateIp string `json:"privateIp"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/api/VirtualMachine/list-vms-info", nil, &resp); err != nil {
		return diag.FromErr(err)
	}

	want := d.Get("name").(string)
	for _, vm := range resp.Data {
		name := vm.VMName
		if i := strings.LastIndex(name, "#"); i >= 0 {
			name = name[i+1:]
		}
		if name == want {
			_ = d.Set("status", vm.Status)
			_ = d.Set("region", vm.RegionId)
			_ = d.Set("private_ip", vm.PrivateIp)
			d.SetId(name)
			return nil
		}
	}
	return diag.Errorf("no virtual machine named %q", want)
}
