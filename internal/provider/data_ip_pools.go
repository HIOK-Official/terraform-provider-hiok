package provider

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// dataSourceIPPools exposes the address ranges a region can hand out.
//
// Worth reading before allocating: a pool that is not assignable exists only to be
// described. A region's IPv4 is a single provider address and it is the host's own,
// so an allocation against it fails with the reason rather than returning nothing —
// this lets a plan branch on that instead of discovering it at apply time.
func dataSourceIPPools() *schema.Resource {
	return &schema.Resource{
		Description: "Public address ranges the platform can hand out in a region.",
		ReadContext: readIPPools,
		Schema: map[string]*schema.Schema{
			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Limit to one region. Omit for every region.",
			},
			"pools": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"id":         {Type: schema.TypeString, Computed: true},
						"region":     {Type: schema.TypeString, Computed: true},
						"family":     {Type: schema.TypeInt, Computed: true},
						"cidr":       {Type: schema.TypeString, Computed: true},
						"gateway":    {Type: schema.TypeString, Computed: true},
						"assignable": {Type: schema.TypeBool, Computed: true},
						"notes":      {Type: schema.TypeString, Computed: true},
						"live_allocations": {
							Type: schema.TypeInt, Computed: true,
							Description: "Addresses currently held from this pool.",
						},
						"usable_addresses": {
							Type: schema.TypeInt, Computed: true,
							Description: "How many addresses the range holds, or 0 when the answer " +
								"is too large to be useful — an IPv6 /64 is 18 quintillion.",
						},
					},
				},
			},
		},
	}
}

func readIPPools(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	path := "/api/Infrastructure/ip-pools"
	region, _ := d.Get("region").(string)
	if region != "" {
		path += "?region=" + url.QueryEscape(region)
	}

	var resp struct {
		Data []struct {
			ID              string `json:"id"`
			Region          string `json:"region"`
			Family          int    `json:"family"`
			Cidr            string `json:"cidr"`
			Gateway         string `json:"gateway"`
			Assignable      bool   `json:"assignable"`
			Notes           string `json:"notes"`
			LiveAllocations int    `json:"liveAllocations"`
			UsableAddresses *int64 `json:"usableAddresses"`
		} `json:"data"`
	}
	if err := c.Do(ctx, "GET", path, nil, &resp); err != nil {
		return diag.FromErr(err)
	}

	pools := make([]map[string]any, 0, len(resp.Data))
	for _, p := range resp.Data {
		usable := 0
		if p.UsableAddresses != nil {
			usable = int(*p.UsableAddresses)
		}
		pools = append(pools, map[string]any{
			"id":               p.ID,
			"region":           p.Region,
			"family":           p.Family,
			"cidr":             p.Cidr,
			"gateway":          p.Gateway,
			"assignable":       p.Assignable,
			"notes":            p.Notes,
			"live_allocations": p.LiveAllocations,
			"usable_addresses": usable,
		})
	}
	_ = d.Set("pools", pools)

	id := "ip-pools"
	if region != "" {
		id += "-" + region
	}
	d.SetId(id + "-" + strconv.FormatInt(time.Now().Unix(), 10))
	return nil
}
