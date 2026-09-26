package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

func resourceVirtualNetwork() *schema.Resource {
	return &schema.Resource{
		Description:   "An isolated virtual network (OVS bridge, optionally VXLAN-backed). Changing any setting other than `timeouts` replaces the network.",
		CreateContext: vnetCreate,
		ReadContext:   vnetRead,
		UpdateContext: vnetUpdate,
		DeleteContext: vnetDelete,
		Importer:      &schema.ResourceImporter{StateContext: importState},
		CustomizeDiff: createTimeOnly("address_space", "region"),

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(10 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"name":          {Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: validateName, Description: "Network name."},
			"address_space": {Type: schema.TypeString, Required: true, ValidateFunc: validation.IsCIDR, Description: "IPv4 CIDR block, e.g. 10.10.0.0/16."},
			"region":        {Type: schema.TypeString, Optional: true, Computed: true, Description: "Region to create the network in, e.g. `canada`. Defaults to the provider's region."},
			"subnet_name": {Type: schema.TypeString, Optional: true, RequiredWith: []string{"subnet_cidr"},
				Description: "Name of the network's subnet. Without it the platform creates a subnet named `default` spanning the whole address space. Changed in place."},
			"subnet_cidr": {Type: schema.TypeString, Optional: true, RequiredWith: []string{"subnet_name"}, ValidateFunc: validation.IsCIDR,
				Description: "IPv4 CIDR of the subnet; must sit inside address_space. Changed in place."},
			"vnet_id":  {Type: schema.TypeString, Computed: true, Description: "Platform ID of the network."},
			"status":   {Type: schema.TypeString, Computed: true, Description: "Provisioning state, e.g. `Available`."},
			"imported": importedSchema(),
		},
	}
}

// vnetInfo is one entry of GET /api/VirtualNetwork/list-vnets.
type vnetInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	RegionId string `json:"regionId"`
	Spaces   []struct {
		IPAddress  string `json:"ipAddress"`
		SubnetMask string `json:"subnetMask"`
	} `json:"vnets"`
	// Older/mock responses carry the CIDR directly.
	AddressSpace string `json:"addressSpace"`
}

// ipv4Space returns the network's first IPv4 address space as CIDR.
func (v *vnetInfo) ipv4Space() string {
	for _, s := range v.Spaces {
		if strings.Contains(s.IPAddress, ".") && s.SubnetMask != "" {
			return s.IPAddress + "/" + s.SubnetMask
		}
	}
	return v.AddressSpace
}

func findVnet(ctx context.Context, c *client.Client, name string) (*vnetInfo, error) {
	var resp struct {
		Data []vnetInfo `json:"data"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/VirtualNetwork/list-vnets", nil, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		if client.VisibleName(resp.Data[i].Name) == name {
			return &resp.Data[i], nil
		}
	}
	return nil, nil
}

func vnetExists(ctx context.Context, c *client.Client, name string) (bool, error) {
	v, err := findVnet(ctx, c, name)
	return v != nil, err
}

type subnetInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	IPRange      string `json:"ipRange"`
	Size         string `json:"size"`
	AddressSpace string `json:"addressSpace"`
}

func listSubnets(ctx context.Context, c *client.Client, vnetID string) ([]subnetInfo, error) {
	var resp struct {
		Data []subnetInfo `json:"data"`
	}
	err := c.Do(ctx, http.MethodGet, "/api/VirtualNetwork/"+url.PathEscape(vnetID)+"/subnets", nil, &resp)
	return resp.Data, err
}

// configureSubnet turns the subnet the platform creates with every network
// ("default", spanning the whole address space) into the requested one.
// create-vnet ignores subnet fields, so this is a second call.
func configureSubnet(ctx context.Context, c *client.Client, vnetID, addressSpace, name, cidr string) error {
	ipRange, size, err := usableRange(cidr)
	if err != nil {
		return err
	}
	body := map[string]any{"name": name, "addressSpace": addressSpace, "size": size, "ipRange": ipRange}
	subnets, err := listSubnets(ctx, c, vnetID)
	if err != nil {
		return err
	}
	if len(subnets) > 0 {
		body["id"] = subnets[0].ID // update the default subnet in place
	}
	if err := c.Do(ctx, http.MethodPut, "/api/VirtualNetwork/"+url.PathEscape(vnetID)+"/subnets", body, nil); err != nil {
		return fmt.Errorf("network created, but configuring subnet %q failed: %w", name, err)
	}
	return nil
}

func vnetCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Get("name").(string)
	addressSpace := d.Get("address_space").(string)

	if cidr, ok := d.GetOk("subnet_cidr"); ok {
		if err := cidrWithin(cidr.(string), addressSpace); err != nil {
			return diag.FromErr(err)
		}
	}
	region, err := resolveRegion(ctx, d, c)
	if err != nil {
		return diag.FromErr(err)
	}
	if existing, err := findVnet(ctx, c, name); err != nil {
		return diag.FromErr(err)
	} else if existing != nil {
		return diag.Errorf("a virtual network named %q already exists; import it with: terraform import <address> %s", name, name)
	}

	payload := map[string]any{
		"name":         name,
		"addressSpace": addressSpace,
		"region":       region,
		"regions":      []string{region},
		"forwardMode":  "nat",
	}
	if err := mergeExtras(d, payload, "hiok_virtual_network"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(ctx, http.MethodPost, "/api/VirtualNetwork/create-vnet", payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(name)
	_ = d.Set("imported", false)
	_ = d.Set("region", region)

	if err := waitForPresence(ctx, c, name, vnetExists, true, d.Timeout(schema.TimeoutCreate)); err != nil {
		return diag.FromErr(err)
	}
	if sn, ok := d.GetOk("subnet_name"); ok {
		v, err := findVnet(ctx, c, name)
		if err != nil {
			return diag.FromErr(err)
		}
		if v != nil && v.ID != "" {
			if err := configureSubnet(ctx, c, v.ID, addressSpace, sn.(string), d.Get("subnet_cidr").(string)); err != nil {
				return diag.FromErr(err)
			}
		}
	}
	return vnetRead(ctx, d, meta)
}

func vnetUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if d.HasChanges("subnet_name", "subnet_cidr") && !d.Get("imported").(bool) {
		name, cidr := d.Get("subnet_name").(string), d.Get("subnet_cidr").(string)
		if name != "" && cidr != "" {
			if err := cidrWithin(cidr, d.Get("address_space").(string)); err != nil {
				return diag.FromErr(err)
			}
			if err := configureSubnet(ctx, c, d.Get("vnet_id").(string), d.Get("address_space").(string), name, cidr); err != nil {
				return diag.FromErr(err)
			}
		}
	}
	return recordOnly(vnetRead)(ctx, d, meta)
}

func vnetRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	v, err := findVnet(ctx, c, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	if v == nil {
		d.SetId("")
		return nil
	}
	_ = d.Set("name", d.Id())
	_ = d.Set("status", v.Status)
	_ = d.Set("vnet_id", v.ID)
	// Only fill what nothing is stored for (after import), so a differently
	// formatted API value can never force a replacement.
	if v.RegionId != "" {
		_ = d.Set("region", v.RegionId)
	}
	if d.Get("imported").(bool) {
		if s := v.ipv4Space(); s != "" && d.Get("address_space").(string) == "" {
			_ = d.Set("address_space", s)
		}
	}
	return nil
}

func vnetDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Id()

	// The endpoint takes the network name as a JSON string body.
	if err := c.Do(ctx, http.MethodDelete, "/api/VirtualNetwork/delete-vnet", name, nil); err != nil {
		// It answers 400 "Failed Deleting Virtual Network." for a network that
		// is already gone; only fail if the network still exists.
		if exists, lookupErr := vnetExists(ctx, c, name); lookupErr != nil || exists {
			return diag.FromErr(err)
		}
	}
	if err := waitForPresence(ctx, c, name, vnetExists, false, d.Timeout(schema.TimeoutDelete)); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

var (
	storageTiers      = []string{"hot", "cool", "cold", "archive"}
	storageRedundancy = []string{"LRS", "ZRS", "GRS", "RA-GRS"}
)

type storageInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DisplayName     string `json:"displayName"`
	Status          string `json:"status"`
	PrimaryRegion   string `json:"primaryRegion"`
	StorageTier     string `json:"storageTier"`
	Redundancy      string `json:"redundancy"`
	PrimaryEndpoint string `json:"primaryEndpoint"`
	QuotaBytes      int64  `json:"quotaBytes"`
}

func findStorage(ctx context.Context, c *client.Client, name string) (*storageInfo, error) {
	var resp struct {
		Data []storageInfo `json:"data"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/StorageAccount", nil, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		if client.VisibleName(resp.Data[i].Name) == name {
			return &resp.Data[i], nil
		}
	}
	return nil, nil
}

func storageExists(ctx context.Context, c *client.Client, name string) (bool, error) {
	s, err := findStorage(ctx, c, name)
	return s != nil, err
}
