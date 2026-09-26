package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// ── Communication service ───────────────────────────────────────────────────

func resourceCommunicationService() *schema.Resource {
	return &schema.Resource{
		Description:   "A Hiok Communication Service: the container for mail domains and senders.",
		CreateContext: commServiceCreate,
		ReadContext:   commServiceRead,
		DeleteContext: commServiceDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":          {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Service name."},
			"region":        {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Primary region."},
			"data_location": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "india", Description: "Where message data is held."},
			"status":        {Type: schema.TypeString, Computed: true},
			"domain_count":  {Type: schema.TypeInt, Computed: true},
		},
	}
}

func commServiceCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"name":         d.Get("name").(string),
		"dataLocation": d.Get("data_location").(string),
	}
	if r := regionOrDefault(d, c); r != "" {
		payload["primaryRegion"] = r
	}

	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.Do(context.Background(), "POST", "/api/Communication/services", payload, &out); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(out.Data.ID)
	return commServiceRead(ctx, d, meta)
}

func commServiceRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var out struct {
		Data struct {
			Name        string `json:"name"`
			Status      string `json:"status"`
			DomainCount int    `json:"domainCount"`
		} `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", "/api/Communication/services/"+d.Id(), nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	_ = d.Set("name", out.Data.Name)
	_ = d.Set("status", out.Data.Status)
	_ = d.Set("domain_count", out.Data.DomainCount)
	return nil
}

func commServiceDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/Communication/services/"+d.Id(), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── Communication domain ────────────────────────────────────────────────────

func resourceCommunicationDomain() *schema.Resource {
	return &schema.Resource{
		Description:   "A sending domain. Managed domains publish their own DNS and verify immediately.",
		CreateContext: commDomainCreate,
		ReadContext:   commDomainRead,
		DeleteContext: commDomainDelete,

		Schema: map[string]*schema.Schema{
			"service_id":      {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Owning communication service."},
			"domain":          {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Label for a managed domain, or the full name for your own."},
			"domain_kind":     {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "managed", Description: "managed or custom."},
			"send_mode":       {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "hiok_mta", Description: "hiok_mta to send through Hiok, relay to use your own provider."},
			"inbound_enabled": {Type: schema.TypeBool, Optional: true, ForceNew: true, Default: false, Description: "Accept incoming mail for this domain."},
			"relay_host":      {Type: schema.TypeString, Optional: true, ForceNew: true},
			"relay_port":      {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 587},
			"relay_username":  {Type: schema.TypeString, Optional: true, ForceNew: true},
			"relay_password":  {Type: schema.TypeString, Optional: true, ForceNew: true, Sensitive: true},
			"fqdn":            {Type: schema.TypeString, Computed: true, Description: "Fully qualified domain that was created."},
			"status":          {Type: schema.TypeString, Computed: true, Description: "verified once its DNS proofs are in place."},
			"dkim_verified":   {Type: schema.TypeBool, Computed: true},
			"spf_verified":    {Type: schema.TypeBool, Computed: true},
		},
	}
}

func commDomainCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	svc := d.Get("service_id").(string)

	payload := map[string]any{
		"domain":         d.Get("domain").(string),
		"domainKind":     d.Get("domain_kind").(string),
		"sendMode":       d.Get("send_mode").(string),
		"inboundEnabled": d.Get("inbound_enabled").(bool),
	}
	if d.Get("send_mode").(string) == "relay" {
		payload["relay"] = map[string]any{
			"host":        d.Get("relay_host").(string),
			"port":        d.Get("relay_port").(int),
			"username":    d.Get("relay_username").(string),
			"password":    d.Get("relay_password").(string),
			"useStartTls": true,
		}
	}

	var out struct {
		Data struct {
			ID     string `json:"id"`
			Domain string `json:"domain"`
		} `json:"data"`
	}
	if err := c.Do(context.Background(), "POST", fmt.Sprintf("/api/Communication/services/%s/domains", svc), payload, &out); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(out.Data.ID)
	_ = d.Set("fqdn", out.Data.Domain)
	return commDomainRead(ctx, d, meta)
}

func commDomainRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var out struct {
		Data []struct {
			ID           string `json:"id"`
			Domain       string `json:"domain"`
			Status       string `json:"status"`
			DkimVerified bool   `json:"dkimVerified"`
			SpfVerified  bool   `json:"spfVerified"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/api/Communication/services/%s/domains", d.Get("service_id").(string))
	if err := c.Do(context.Background(), "GET", path, nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	for _, x := range out.Data {
		if x.ID == d.Id() {
			_ = d.Set("fqdn", x.Domain)
			_ = d.Set("status", x.Status)
			_ = d.Set("dkim_verified", x.DkimVerified)
			_ = d.Set("spf_verified", x.SpfVerified)
			return nil
		}
	}
	d.SetId("")
	return nil
}

func commDomainDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	path := fmt.Sprintf("/api/Communication/services/%s/domains/%s", d.Get("service_id").(string), d.Id())
	if err := c.Do(context.Background(), "DELETE", path, nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── Bastion ─────────────────────────────────────────────────────────────────

func resourceBastion() *schema.Resource {
	return &schema.Resource{
		Description:   "A bastion that joins a virtual network so browsers can reach its VMs.",
		CreateContext: bastionCreate,
		ReadContext:   bastionRead,
		DeleteContext: bastionDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":       {Type: schema.TypeString, Required: true, ForceNew: true},
			"vnet_name":  {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Virtual network to serve."},
			"address":    {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Address on that network in CIDR form, e.g. 10.0.0.9/24."},
			"gateway":    {Type: schema.TypeString, Optional: true, ForceNew: true},
			"region":     {Type: schema.TypeString, Optional: true, ForceNew: true},
			"sku":        {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "standard"},
			"attached":   {Type: schema.TypeBool, Computed: true, Description: "True once the daemon holds an interface on the network."},
			"status":     {Type: schema.TypeString, Computed: true},
			"portal_url": {Type: schema.TypeString, Computed: true, Description: "Where browser sessions are opened."},
		},
	}
}

func bastionCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"name":     d.Get("name").(string),
		"vnetName": d.Get("vnet_name").(string),
		"address":  d.Get("address").(string),
		"sku":      d.Get("sku").(string),
	}
	if g, ok := d.GetOk("gateway"); ok {
		payload["gateway"] = g.(string)
	}
	if r := regionOrDefault(d, c); r != "" {
		payload["region"] = r
	}

	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := mergeExtras(d, payload, "hiok_bastion"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/Bastion", payload, &out); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(out.Data.ID)
	return bastionRead(ctx, d, meta)
}

func bastionRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var out struct {
		Data struct {
			Name      string `json:"name"`
			Attached  bool   `json:"attached"`
			Status    string `json:"status"`
			PortalURL string `json:"portalUrl"`
		} `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", "/api/Bastion/"+d.Id(), nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	_ = d.Set("name", out.Data.Name)
	_ = d.Set("attached", out.Data.Attached)
	_ = d.Set("status", out.Data.Status)
	_ = d.Set("portal_url", out.Data.PortalURL)
	return nil
}

func bastionDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/Bastion/"+d.Id(), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
