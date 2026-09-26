package provider

import (
	"context"
	"net/url"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// resourceKeyVault manages a vault. Deletion through Terraform is deliberately soft:
// destroying the resource starts the recovery window rather than destroying the
// material, which is the whole point of the retention setting. Purging is a separate,
// explicit act — a `terraform destroy` should never be able to lose secrets outright.
func resourceKeyVault() *schema.Resource {
	return &schema.Resource{
		Description: "A key vault holding secrets, keys and certificates, replicated to the " +
			"regions you choose. Destroying this resource soft-deletes the vault; its contents " +
			"stay recoverable until the retention window ends.",
		CreateContext: keyVaultCreate,
		ReadContext:   keyVaultRead,
		UpdateContext: keyVaultUpdate,
		DeleteContext: keyVaultDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name": {
				Type: schema.TypeString, Required: true, ForceNew: true,
				Description: "3-63 characters of lowercase letters, digits or hyphens. It becomes " +
					"part of the vault's URI, so it has to be a valid DNS label.",
			},
			"primary_region": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Region that answers reads and writes. Defaults to the provider's first region.",
			},
			"regions": {
				Type: schema.TypeList, Optional: true, Elem: &schema.Schema{Type: schema.TypeString},
				Description: "Regions holding a replica. The primary is included automatically.",
			},
			"vnet_id": {
				Type: schema.TypeString, Optional: true,
				Description: "Restrict the vault to a virtual network. Leave unset for a vault " +
					"reachable wherever the caller can authenticate — both are the same plan.",
			},
			"vnet_name": {Type: schema.TypeString, Optional: true, Description: "Name of that virtual network."},
			"allowed_cidrs": {
				Type: schema.TypeList, Optional: true, Elem: &schema.Schema{Type: schema.TypeString},
				Description: "Addresses permitted to reach a network-restricted vault.",
			},
			"soft_delete_retention_days": {
				Type: schema.TypeInt, Optional: true, Default: 90,
				Description: "How long a deleted vault stays recoverable. Clamped to 7-365.",
			},
			"purge_protection": {
				Type: schema.TypeBool, Optional: true, Default: false,
				Description: "Prevents an early purge. This can be switched on but never off, so " +
					"changing it back to false will be rejected by the API.",
			},

			"status":             {Type: schema.TypeString, Computed: true},
			"vault_uri":          {Type: schema.TypeString, Computed: true, Description: "Endpoint applications address the vault by."},
			"item_count":         {Type: schema.TypeInt, Computed: true},
			"network_restricted": {Type: schema.TypeBool, Computed: true},
		},
	}
}

type keyVault struct {
	ID                      string   `json:"id"`
	Name                    string   `json:"name"`
	PrimaryRegion           string   `json:"primaryRegion"`
	Regions                 []string `json:"regions"`
	Status                  string   `json:"status"`
	VnetID                  string   `json:"vnetId"`
	VnetName                string   `json:"vnetName"`
	AllowedCidrs            []string `json:"allowedCidrs"`
	NetworkRestricted       bool     `json:"networkRestricted"`
	SoftDeleteRetentionDays int      `json:"softDeleteRetentionDays"`
	PurgeProtection         bool     `json:"purgeProtection"`
	VaultURI                string   `json:"vaultUri"`
	ItemCount               int      `json:"itemCount"`
}

func keyVaultApply(d *schema.ResourceData, v *keyVault) {
	_ = d.Set("name", v.Name)
	_ = d.Set("primary_region", v.PrimaryRegion)
	_ = d.Set("regions", v.Regions)
	_ = d.Set("status", v.Status)
	_ = d.Set("vnet_name", v.VnetName)
	_ = d.Set("allowed_cidrs", v.AllowedCidrs)
	_ = d.Set("network_restricted", v.NetworkRestricted)
	_ = d.Set("soft_delete_retention_days", v.SoftDeleteRetentionDays)
	_ = d.Set("purge_protection", v.PurgeProtection)
	_ = d.Set("vault_uri", v.VaultURI)
	_ = d.Set("item_count", v.ItemCount)
	if v.VnetID != "" {
		_ = d.Set("vnet_id", v.VnetID)
	}
}

func keyVaultCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	region := d.Get("primary_region").(string)
	if region == "" && len(c.Regions) > 0 {
		region = c.Regions[0]
	}

	payload := map[string]any{
		"name":                    d.Get("name").(string),
		"primaryRegion":           region,
		"softDeleteRetentionDays": d.Get("soft_delete_retention_days").(int),
		"purgeProtection":         d.Get("purge_protection").(bool),
	}
	if regions := stringList(d, "regions"); len(regions) > 0 {
		payload["regions"] = regions
	}
	if cidrs := stringList(d, "allowed_cidrs"); len(cidrs) > 0 {
		payload["allowedCidrs"] = cidrs
	}
	if v, ok := d.GetOk("vnet_id"); ok && v.(string) != "" {
		payload["vnetId"] = v.(string)
	}
	if v, ok := d.GetOk("vnet_name"); ok && v.(string) != "" {
		payload["vnetName"] = v.(string)
	}

	var vault keyVault
	if err := c.Do(context.Background(), "POST", "/api/KeyVault", payload, &vault); err != nil {
		return diag.FromErr(err)
	}
	if vault.ID == "" {
		return diag.Errorf("the API accepted the request but returned no vault")
	}

	d.SetId(vault.ID)
	keyVaultApply(d, &vault)
	return nil
}

func keyVaultRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var vault keyVault
	if err := c.Do(context.Background(), "GET", "/api/KeyVault/"+url.PathEscape(d.Id()), nil, &vault); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		// A vault that is gone from the API is gone from state; the alternative is a
		// plan that can never converge.
		d.SetId("")
		return nil
	}
	keyVaultApply(d, &vault)
	return nil
}

func keyVaultUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	payload := map[string]any{
		"softDeleteRetentionDays": d.Get("soft_delete_retention_days").(int),
		"purgeProtection":         d.Get("purge_protection").(bool),
	}
	if regions := stringList(d, "regions"); len(regions) > 0 {
		payload["regions"] = regions
	}
	if cidrs := stringList(d, "allowed_cidrs"); len(cidrs) > 0 {
		payload["allowedCidrs"] = cidrs
	}
	if v, ok := d.GetOk("vnet_id"); ok && v.(string) != "" {
		payload["vnetId"] = v.(string)
	}
	if v, ok := d.GetOk("vnet_name"); ok && v.(string) != "" {
		payload["vnetName"] = v.(string)
	}

	var vault keyVault
	if err := c.Do(context.Background(), "PUT", "/api/KeyVault/"+url.PathEscape(d.Id()), payload, &vault); err != nil {
		return diag.FromErr(err)
	}
	keyVaultApply(d, &vault)
	return nil
}

func keyVaultDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	// Soft delete only. Purging is not wired to destroy on purpose: a mistaken
	// `terraform destroy` must stay recoverable.
	if err := c.Do(context.Background(), "DELETE", "/api/KeyVault/"+url.PathEscape(d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
