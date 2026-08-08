package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

func resourceVirtualMachine() *schema.Resource {
	return &schema.Resource{
		Description:   "A KVM virtual machine.",
		CreateContext: vmCreate,
		ReadContext:   vmRead,
		DeleteContext: vmDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Virtual machine name; unique within the account.",
			},
			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "Region to deploy into. Defaults to the provider's first region.",
			},
			"image": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Default:     "ubuntu-24.04",
				Description: "Base image id, as reported by the hiok_vm_images data source.",
			},
			"vcpu_count": {
				Type:        schema.TypeInt,
				Optional:    true,
				ForceNew:    true,
				Default:     1,
				Description: "Virtual CPUs.",
			},
			"ram_gb": {
				Type:        schema.TypeFloat,
				Optional:    true,
				ForceNew:    true,
				Default:     1,
				Description: "Memory in GiB.",
			},
			"network_name": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Default:     "default",
				Description: "libvirt network or OVS bridge to attach to.",
			},
			"username": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "cloud-init user created on first boot.",
			},
			"ssh_public_key": {
				Type:        schema.TypeString,
				Optional:    true,
				ForceNew:    true,
				Description: "Public key authorised for the cloud-init user.",
			},
			"generate_ssh_key": {
				Type:        schema.TypeBool,
				Optional:    true,
				ForceNew:    true,
				Description: "Have the platform generate a keypair; fetch the private key from the console.",
			},
			"status": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Current power state.",
			},
			"private_ip": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Address on the attached network.",
			},
		},
	}
}

func vmRegions(d *schema.ResourceData, c *client.Client) []string {
	if r, ok := d.GetOk("region"); ok && r.(string) != "" {
		return []string{r.(string)}
	}
	return c.Regions
}

func vmCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	payload := map[string]any{
		"vmName":         d.Get("name").(string),
		"regions":        vmRegions(d, c),
		"sourceFilePath": d.Get("image").(string),
		"vcpuCount":      d.Get("vcpu_count").(int),
		"ramSize":        d.Get("ram_gb").(float64),
		"networkName":    d.Get("network_name").(string),
	}
	if v, ok := d.GetOk("username"); ok {
		payload["username"] = v.(string)
	}
	if v, ok := d.GetOk("ssh_public_key"); ok {
		payload["sshPublicKey"] = v.(string)
		payload["authType"] = "ssh"
	}
	if d.Get("generate_ssh_key").(bool) {
		payload["generateSshKey"] = true
		payload["authType"] = "ssh"
	}

	if err := c.Do("POST", "/api/VirtualMachine/create-vm", payload, nil); err != nil {
		return diag.FromErr(err)
	}

	d.SetId(d.Get("name").(string))
	return vmRead(ctx, d, meta)
}

func vmRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
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

	want := d.Id()
	for _, vm := range resp.Data {
		// Names are stored scoped ("<owner>#<name>"); compare on the visible part.
		name := vm.VMName
		if i := strings.LastIndex(name, "#"); i >= 0 {
			name = name[i+1:]
		}
		if name == want {
			_ = d.Set("name", name)
			_ = d.Set("status", vm.Status)
			_ = d.Set("private_ip", vm.PrivateIp)
			if vm.RegionId != "" {
				_ = d.Set("region", vm.RegionId)
			}
			return nil
		}
	}

	// Gone outside Terraform: drop it from state rather than failing the plan.
	d.SetId("")
	return nil
}

func vmDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	path := fmt.Sprintf("/api/VirtualMachine/destroy-vm?vmName=%s", d.Id())
	if err := c.Do("DELETE", path, nil, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
