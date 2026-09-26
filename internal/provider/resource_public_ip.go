package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// resourcePublicIP hands a guest a real routable address from the region's
// provider-routed prefix.
//
// Every field is ForceNew. An allocation IS an address: changing which guest holds
// it, or which name it answers to, is not an edit to the same object — the platform
// has to unconfigure the old host, hand back the address and take a new one. Making
// that look like an in-place update would hide a change of address from whoever
// depends on it.
func resourcePublicIP() *schema.Resource {
	return &schema.Resource{
		Description: "A public IP address allocated from the region's routed prefix and " +
			"configured on the host so it reaches a container or virtual machine.",
		CreateContext: publicIPCreate,
		ReadContext:   publicIPRead,
		DeleteContext: publicIPDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"region": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Region whose pool the address comes from. Defaults to the provider's first region.",
			},
			"family": {
				Type: schema.TypeInt, Optional: true, Default: 6, ForceNew: true,
				Description: "4 or 6. Only IPv6 is assignable today: a region's IPv4 is a single " +
					"provider address and it is the host's own, so there is nothing to hand out.",
			},
			"resource_kind": {
				Type: schema.TypeString, Optional: true, Default: "manual", ForceNew: true,
				Description: "What holds the address: virtualmachine, container, bastion, …",
			},
			"resource_id": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Identifier of the resource holding the address.",
			},
			"resource_name": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Display name of the resource holding the address.",
			},
			"target_name": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "The name the HOST knows the guest by: the Docker container name, or " +
					"the libvirt domain (which for a VM is \"<guid>#<name>\"). Leave empty to " +
					"reserve the address without configuring any host.",
			},
			"target_kind": {
				Type: schema.TypeString, Optional: true, Default: "container", ForceNew: true,
				Description: "\"container\" or \"vm\". A container is configured by entering its " +
					"namespace; a VM is reached through its guest agent, so they are not interchangeable.",
			},
			"hostname": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Publish DNS for the address under this name. A bare label lands in the " +
					"managed zone; a fully-qualified name is used as given.",
			},
			"set_reverse_dns": {
				Type: schema.TypeBool, Optional: true, Default: true, ForceNew: true,
				Description: "Publish reverse DNS as well as the forward record. The provider " +
					"forward-confirms, so this can only succeed once the forward record is live.",
			},

			"address":       {Type: schema.TypeString, Computed: true, Description: "The allocated address."},
			"prefix_length": {Type: schema.TypeInt, Computed: true, Description: "Prefix length of the guest's address."},
			"gateway":       {Type: schema.TypeString, Computed: true, Description: "Upstream gateway for the pool."},
			"configured": {
				Type: schema.TypeBool, Computed: true,
				Description: "True once the host actually carries the route and NDP proxy entry.",
			},
			"status_message": {
				Type: schema.TypeString, Computed: true,
				Description: "Why the address is not fully configured, when it is not. A VM whose " +
					"image has no QEMU guest agent gets the host half only, and says so here.",
			},
			"ptr_status": {
				Type: schema.TypeString, Computed: true,
				Description: "Why reverse DNS was not published, when it was not.",
			},
		},
	}
}

type publicIPAllocation struct {
	ID            string `json:"id"`
	Region        string `json:"region"`
	Family        int    `json:"family"`
	Address       string `json:"address"`
	PrefixLength  int    `json:"prefixLength"`
	Gateway       string `json:"gateway"`
	ResourceKind  string `json:"resourceKind"`
	ResourceID    string `json:"resourceId"`
	ResourceName  string `json:"resourceName"`
	TargetKind    string `json:"targetKind"`
	TargetName    string `json:"targetName"`
	Hostname      string `json:"hostname"`
	PtrStatus     string `json:"ptrStatus"`
	Configured    bool   `json:"configured"`
	StatusMessage string `json:"statusMessage"`
	ReleasedAt    string `json:"releasedAt"`
}

func publicIPRegion(d *schema.ResourceData, c *client.Client) string {
	if r, ok := d.GetOk("region"); ok && r.(string) != "" {
		return r.(string)
	}
	if len(c.Regions) > 0 {
		return c.Regions[0]
	}
	return "canada"
}

func publicIPApply(d *schema.ResourceData, a *publicIPAllocation) {
	_ = d.Set("region", a.Region)
	_ = d.Set("family", a.Family)
	_ = d.Set("address", a.Address)
	_ = d.Set("prefix_length", a.PrefixLength)
	_ = d.Set("gateway", a.Gateway)
	_ = d.Set("resource_kind", a.ResourceKind)
	_ = d.Set("resource_id", a.ResourceID)
	_ = d.Set("resource_name", a.ResourceName)
	_ = d.Set("target_kind", a.TargetKind)
	_ = d.Set("target_name", a.TargetName)
	_ = d.Set("hostname", a.Hostname)
	_ = d.Set("configured", a.Configured)
	_ = d.Set("status_message", a.StatusMessage)
	_ = d.Set("ptr_status", a.PtrStatus)
}

func publicIPCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	payload := map[string]any{
		"region":        publicIPRegion(d, c),
		"family":        d.Get("family").(int),
		"resourceKind":  d.Get("resource_kind").(string),
		"targetKind":    d.Get("target_kind").(string),
		"setReverseDns": d.Get("set_reverse_dns").(bool),
	}
	for key, field := range map[string]string{
		"resourceId":      "resource_id",
		"resourceName":    "resource_name",
		"targetContainer": "target_name",
		"hostname":        "hostname",
	} {
		if v, ok := d.GetOk(field); ok && v.(string) != "" {
			payload[key] = v.(string)
		}
	}

	var resp struct {
		Data publicIPAllocation `json:"data"`
	}
	if err := c.Do(context.Background(), "POST", "/api/Infrastructure/ip-allocations", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	if resp.Data.ID == "" {
		return diag.Errorf("the API accepted the request but returned no allocation")
	}

	d.SetId(resp.Data.ID)
	publicIPApply(d, &resp.Data)

	// A partially applied address is usable and worth keeping in state, but silently
	// reporting success would leave the operator to discover it by pinging.
	var diags diag.Diagnostics
	if !resp.Data.Configured && resp.Data.StatusMessage != "" {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Warning,
			Summary:  fmt.Sprintf("%s was allocated but not fully configured", resp.Data.Address),
			Detail:   resp.Data.StatusMessage,
		})
	}
	if resp.Data.PtrStatus != "" {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Warning,
			Summary:  fmt.Sprintf("Reverse DNS for %s is not published", resp.Data.Address),
			Detail:   resp.Data.PtrStatus,
		})
	}
	return diags
}

func publicIPRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	// There is no by-id read, so the region's live list is filtered. Released rows are
	// deliberately excluded: an address handed back is gone as far as state is
	// concerned, even though the row survives for the record.
	path := "/api/Infrastructure/ip-allocations"
	if region, ok := d.GetOk("region"); ok && region.(string) != "" {
		path += "?region=" + url.QueryEscape(region.(string))
	}

	var resp struct {
		Data []publicIPAllocation `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", path, nil, &resp); err != nil {
		return diag.FromErr(err)
	}
	for i := range resp.Data {
		if resp.Data[i].ID == d.Id() && resp.Data[i].ReleasedAt == "" {
			publicIPApply(d, &resp.Data[i])
			return nil
		}
	}

	d.SetId("")
	return nil
}

func publicIPDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	path := "/api/Infrastructure/ip-allocations/" + d.Id()
	if v, ok := d.GetOk("target_name"); ok && v.(string) != "" {
		path += "?targetContainer=" + url.QueryEscape(v.(string))
	}
	if err := c.Do(context.Background(), "DELETE", path, nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
