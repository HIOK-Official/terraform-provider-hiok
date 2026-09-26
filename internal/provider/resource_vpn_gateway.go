package provider

import (
	"context"
	"net/url"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// resourceVPNGateway manages a VPN gateway. Everything is ForceNew: a gateway's
// identity is its on-host PKI and its listening endpoint, so changing the address
// pool or protocol means issuing a new certificate authority — not editing one.
func resourceVPNGateway() *schema.Resource {
	return &schema.Resource{
		Description: "A VPN gateway. Point-to-Site issues OpenVPN client profiles; " +
			"Site-to-Site builds WireGuard tunnels to remote networks.",
		CreateContext: vpnGatewayCreate,
		ReadContext:   vpnGatewayRead,
		DeleteContext: vpnGatewayDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":   {Type: schema.TypeString, Required: true, ForceNew: true},
			"region": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"gateway_type": {
				Type: schema.TypeInt, Optional: true, Default: 1, ForceNew: true,
				Description: "0 for Site-to-Site (WireGuard), 1 for Point-to-Site (OpenVPN).",
			},
			"vnet_id":   {Type: schema.TypeString, Optional: true, ForceNew: true},
			"vnet_name": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"address_pool": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Range handed to connecting clients, for example 10.71.0.0/24.",
			},
			"protocol": {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "\"openvpn\" or \"wireguard\"."},
			"port":     {Type: schema.TypeInt, Optional: true, ForceNew: true},
			"dns_servers": {
				Type: schema.TypeList, Optional: true, ForceNew: true,
				Elem: &schema.Schema{Type: schema.TypeString},
			},
			"split_tunneling": {Type: schema.TypeBool, Optional: true, Default: true, ForceNew: true},

			"status":            {Type: schema.TypeString, Computed: true},
			"public_ip_address": {Type: schema.TypeString, Computed: true, Description: "Address clients dial."},
			"vpn_configuration": {
				Type: schema.TypeString, Computed: true, Sensitive: true,
				Description: "A ready-to-use client profile generated with the gateway. It embeds " +
					"private key material, so it is marked sensitive.",
			},
		},
	}
}

type vpnGateway struct {
	ID               string `json:"id"`
	VPNGatewayName   string `json:"vpnGatewayName"`
	RegionID         string `json:"regionId"`
	Status           string `json:"status"`
	PublicIPAddress  string `json:"publicIPAddress"`
	VPNConfiguration string `json:"vpnConfiguration"`
	AddressPool      string `json:"addressPool"`
	Protocol         string `json:"protocol"`
	ListenPort       int    `json:"listenPort"`
	GatewayType      int    `json:"gatewayType"`
}

func vpnGatewayApply(d *schema.ResourceData, g *vpnGateway) {
	_ = d.Set("name", g.VPNGatewayName)
	_ = d.Set("status", g.Status)
	_ = d.Set("public_ip_address", g.PublicIPAddress)
	_ = d.Set("vpn_configuration", g.VPNConfiguration)
	if g.RegionID != "" {
		_ = d.Set("region", g.RegionID)
	}
	if g.AddressPool != "" {
		_ = d.Set("address_pool", g.AddressPool)
	}
	if g.Protocol != "" {
		_ = d.Set("protocol", g.Protocol)
	}
	if g.ListenPort != 0 {
		_ = d.Set("port", g.ListenPort)
	}
}

func vpnGatewayCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	region := d.Get("region").(string)
	if region == "" && len(c.Regions) > 0 {
		region = c.Regions[0]
	}

	payload := map[string]any{
		"name":           d.Get("name").(string),
		"region":         region,
		"gatewayType":    d.Get("gateway_type").(int),
		"splitTunneling": d.Get("split_tunneling").(bool),
	}
	for key, field := range map[string]string{
		"vNetId":      "vnet_id",
		"vNetName":    "vnet_name",
		"addressPool": "address_pool",
		"protocol":    "protocol",
	} {
		if v, ok := d.GetOk(field); ok && v.(string) != "" {
			payload[key] = v.(string)
		}
	}
	if v, ok := d.GetOk("port"); ok && v.(int) != 0 {
		payload["port"] = v.(int)
	}
	if dns := stringList(d, "dns_servers"); len(dns) > 0 {
		payload["dnsServers"] = dns
	}

	var resp struct {
		Data vpnGateway `json:"data"`
	}
	if err := mergeExtras(d, payload, "hiok_vpn_gateway"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/VPNGateway/create", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	if resp.Data.ID == "" {
		return diag.Errorf("the API accepted the request but returned no gateway")
	}

	d.SetId(resp.Data.ID)
	vpnGatewayApply(d, &resp.Data)
	return nil
}

func vpnGatewayRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data vpnGateway `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", "/api/VPNGateway/"+url.PathEscape(d.Id()), nil, &resp); err != nil || resp.Data.ID == "" {
		if err != nil && !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	vpnGatewayApply(d, &resp.Data)
	return nil
}

func vpnGatewayDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	// The processor addresses a gateway by name, so the delete carries both.
	path := "/api/VPNGateway/" + url.PathEscape(d.Id()) +
		"?gatewayName=" + url.QueryEscape(d.Get("name").(string))
	if region := d.Get("region").(string); region != "" {
		path += "&region=" + url.QueryEscape(region)
	}
	if err := c.Do(context.Background(), "DELETE", path, nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// resourceVPNClient issues a Point-to-Site client certificate.
//
// The profile is fetched on read rather than stored at create, so it always reflects
// the certificate that is live now. Revoking is the destroy: the record stays on the
// gateway as revoked and its name stays taken, which is what stops a withdrawn
// identity from being reissued.
func resourceVPNClient() *schema.Resource {
	return &schema.Resource{
		Description:   "A Point-to-Site VPN client certificate and its connection profile.",
		CreateContext: vpnClientCreate,
		ReadContext:   vpnClientRead,
		DeleteContext: vpnClientDelete,

		Schema: map[string]*schema.Schema{
			"gateway_id": {Type: schema.TypeString, Required: true, ForceNew: true},
			"name":       {Type: schema.TypeString, Required: true, ForceNew: true},
			"email":      {Type: schema.TypeString, Optional: true, ForceNew: true},
			"region":     {Type: schema.TypeString, Optional: true, ForceNew: true},

			"status": {Type: schema.TypeString, Computed: true},
			"config": {
				Type: schema.TypeString, Computed: true, Sensitive: true,
				Description: "The client profile, embedding the CA, the client certificate and its " +
					"private key. Write it to a file and hand it to the client.",
			},
		},
	}
}

type vpnClient struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func vpnClientRegionQuery(d *schema.ResourceData) string {
	if region := d.Get("region").(string); region != "" {
		return "?region=" + url.QueryEscape(region)
	}
	return ""
}

func vpnClientCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	payload := map[string]any{
		"name":      d.Get("name").(string),
		"gatewayId": d.Get("gateway_id").(string),
	}
	if v, ok := d.GetOk("email"); ok && v.(string) != "" {
		payload["emailId"] = v.(string)
	}

	var client_ vpnClient
	if err := mergeExtras(d, payload, "hiok_vpn_client"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/VPNGateway/clients/p2s"+vpnClientRegionQuery(d), payload, &client_); err != nil {
		return diag.FromErr(err)
	}
	if client_.ID == "" {
		return diag.Errorf("the API accepted the request but returned no client")
	}

	d.SetId(client_.ID)
	_ = d.Set("status", client_.Status)
	return vpnClientRead(ctx, d, meta)
}

func vpnClientRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	// The config endpoint answers with the profile itself, not JSON.
	raw, err := c.DoRaw("GET", "/api/VPNGateway/clients/p2s/"+url.PathEscape(d.Id())+"/config"+vpnClientRegionQuery(d))
	if err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		// A client that no longer has a profile has been revoked or removed upstream.
		d.SetId("")
		return nil
	}
	_ = d.Set("config", string(raw))
	return nil
}

func vpnClientDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/VPNGateway/clients/p2s/"+url.PathEscape(d.Id())+vpnClientRegionQuery(d), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
