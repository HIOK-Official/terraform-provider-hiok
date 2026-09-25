package provider

import (
	"context"
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
		UpdateContext: recordOnly(vnetRead),
		DeleteContext: vnetDelete,
		Importer:      &schema.ResourceImporter{StateContext: importState},
		CustomizeDiff: createTimeOnly("address_space", "region", "subnet_name", "subnet_cidr"),

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(10 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"name":          {Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: validateName, Description: "Network name."},
			"address_space": {Type: schema.TypeString, Required: true, ValidateFunc: validation.IsCIDR, Description: "CIDR block, e.g. 10.10.0.0/16."},
			"region":        {Type: schema.TypeString, Optional: true, Description: "Region to create the network in. Defaults to the provider's regions."},
			"subnet_name":   {Type: schema.TypeString, Optional: true, RequiredWith: []string{"subnet_cidr"}, Description: "Name of the first subnet."},
			"subnet_cidr":   {Type: schema.TypeString, Optional: true, RequiredWith: []string{"subnet_name"}, ValidateFunc: validation.IsCIDR, Description: "CIDR of the first subnet; must sit inside address_space."},
			"status":        {Type: schema.TypeString, Computed: true, Description: "Provisioning state."},
			"imported":      importedSchema(),
		},
	}
}

type vnetInfo struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	AddressSpace string `json:"addressSpace"`
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

func vnetCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Get("name").(string)

	if existing, err := findVnet(ctx, c, name); err != nil {
		return diag.FromErr(err)
	} else if existing != nil {
		return diag.Errorf("a virtual network named %q already exists; import it with: terraform import <address> %s", name, name)
	}
	if cidr, ok := d.GetOk("subnet_cidr"); ok {
		if err := cidrWithin(cidr.(string), d.Get("address_space").(string)); err != nil {
			return diag.FromErr(err)
		}
	}

	payload := map[string]any{
		"name":         name,
		"addressSpace": d.Get("address_space").(string),
		"regions":      regionsFor(d, c),
		"forwardMode":  "nat",
	}
	if v, ok := d.GetOk("subnet_name"); ok {
		payload["subnetName"] = v.(string)
	}
	if v, ok := d.GetOk("subnet_cidr"); ok {
		payload["subnetCidr"] = v.(string)
	}

	if err := c.Do(ctx, http.MethodPost, "/api/VirtualNetwork/create-vnet", payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(name)
	_ = d.Set("imported", false)

	if err := waitForPresence(ctx, c, name, vnetExists, true, d.Timeout(schema.TimeoutCreate)); err != nil {
		return diag.FromErr(err)
	}
	return vnetRead(ctx, d, meta)
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
	// Only fill address_space when nothing is stored (after import), so a
	// differently formatted API value can never force a replacement.
	if v.AddressSpace != "" && d.Get("address_space").(string) == "" {
		_ = d.Set("address_space", v.AddressSpace)
	}
	return nil
}

func vnetDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Id()

	// The endpoint takes the network name as a JSON string body; also pass it
	// as a query parameter in case a proxy strips DELETE bodies.
	err := c.Do(ctx, http.MethodDelete, "/api/VirtualNetwork/delete-vnet"+client.Query("name", name), name, nil)
	if err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	if err := waitForPresence(ctx, c, name, vnetExists, false, d.Timeout(schema.TimeoutDelete)); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

func resourceStorageAccount() *schema.Resource {
	return &schema.Resource{
		Description:   "A storage account. Changing any setting other than `timeouts` replaces the account and its data.",
		CreateContext: storageCreate,
		ReadContext:   storageRead,
		UpdateContext: recordOnly(storageRead),
		DeleteContext: storageDelete,
		Importer:      &schema.ResourceImporter{StateContext: importState},
		CustomizeDiff: createTimeOnly("region", "tier", "redundancy"),

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(10 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"name":       {Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: validateName, Description: "Storage account name."},
			"region":     {Type: schema.TypeString, Optional: true, Description: "Primary region. Defaults to the provider's first region."},
			"tier":       {Type: schema.TypeString, Optional: true, Default: "standard", ValidateFunc: validation.StringIsNotWhiteSpace, Description: "Performance tier."},
			"redundancy": {Type: schema.TypeString, Optional: true, Default: "lrs", ValidateFunc: validation.StringIsNotWhiteSpace, Description: "Redundancy mode (sent upper-cased, e.g. LRS)."},
			"status":     {Type: schema.TypeString, Computed: true, Description: "Provisioning state."},
			"imported":   importedSchema(),
		},
	}
}

type storageInfo struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	PrimaryRegion string `json:"primaryRegion"`
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

func storageCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Get("name").(string)

	if existing, err := findStorage(ctx, c, name); err != nil {
		return diag.FromErr(err)
	} else if existing != nil {
		return diag.Errorf("a storage account named %q already exists; import it with: terraform import <address> %s", name, name)
	}

	region := firstRegion(d, c)
	payload := map[string]any{
		"name":                 name,
		"displayName":          name,
		"primaryRegion":        region,
		"preferredReadRegion":  region,
		"replicaRegions":       []string{},
		"storageTier":          d.Get("tier").(string),
		"redundancy":           strings.ToUpper(d.Get("redundancy").(string)),
		"consistencyMode":      "session",
		"writeAcknowledgement": "quorum",
	}
	if err := c.Do(ctx, http.MethodPost, "/api/StorageAccount", payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(name)
	_ = d.Set("imported", false)

	if err := waitForPresence(ctx, c, name, storageExists, true, d.Timeout(schema.TimeoutCreate)); err != nil {
		return diag.FromErr(err)
	}
	return storageRead(ctx, d, meta)
}

func storageRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	s, err := findStorage(ctx, c, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	if s == nil {
		d.SetId("")
		return nil
	}
	_ = d.Set("name", d.Id())
	_ = d.Set("status", s.Status)
	return nil
}

func storageDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Id()

	err := c.Do(ctx, http.MethodDelete, "/api/StorageAccount/"+url.PathEscape(name), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	if err := waitForPresence(ctx, c, name, storageExists, false, d.Timeout(schema.TimeoutDelete)); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
