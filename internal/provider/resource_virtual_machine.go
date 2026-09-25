package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

func resourceVirtualMachine() *schema.Resource {
	return &schema.Resource{
		Description: "A KVM virtual machine. The HIOK API has no in-place update for virtual machines, " +
			"so changing any setting other than `timeouts` destroys the VM (including its disk) and creates a new one.",
		CreateContext: vmCreate,
		ReadContext:   vmRead,
		UpdateContext: recordOnly(vmRead),
		DeleteContext: vmDelete,
		Importer:      &schema.ResourceImporter{StateContext: importState},
		CustomizeDiff: createTimeOnly("image", "vcpu_count", "ram_gb", "network_name", "username", "ssh_public_key", "generate_ssh_key"),

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(20 * time.Minute),
			Delete: schema.DefaultTimeout(15 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validateName,
				Description:  "Virtual machine name; unique within the account.",
			},
			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				Computed:    true,
				ForceNew:    true,
				Description: "Region to deploy into. Defaults to the provider's first region.",
			},
			"image": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "ubuntu-24.04",
				ValidateFunc: validation.StringIsNotWhiteSpace,
				Description:  "Base image, sent to the API as `sourceFilePath`.",
			},
			"vcpu_count": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      1,
				ValidateFunc: validation.IntBetween(1, 128),
				Description:  "Virtual CPUs.",
			},
			"ram_gb": {
				Type:         schema.TypeFloat,
				Optional:     true,
				Default:      1.0,
				ValidateFunc: validation.FloatBetween(0.5, 1024),
				Description:  "Memory in GiB.",
			},
			"network_name": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "default",
				ValidateFunc: validation.StringIsNotWhiteSpace,
				Description:  "Virtual network (libvirt network or OVS bridge) to attach to.",
			},
			"username": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "cloud-init user created on first boot.",
			},
			"ssh_public_key": {
				Type:          schema.TypeString,
				Optional:      true,
				ConflictsWith: []string{"generate_ssh_key"},
				Description:   "Public key authorised for the cloud-init user.",
			},
			"generate_ssh_key": {
				Type:          schema.TypeBool,
				Optional:      true,
				ConflictsWith: []string{"ssh_public_key"},
				Description:   "Have the platform generate a keypair; download the private key from the HIOK console.",
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
			"imported": importedSchema(),
		},
	}
}

type vmInfo struct {
	VMName    string `json:"vmName"`
	Status    string `json:"status"`
	RegionId  string `json:"regionId"`
	PrivateIp string `json:"privateIp"`
}

func findVM(ctx context.Context, c *client.Client, name string) (*vmInfo, error) {
	var resp struct {
		Data []vmInfo `json:"data"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/VirtualMachine/list-vms-info", nil, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		// Names are stored scoped ("<owner>#<name>"); compare on the visible part.
		if client.VisibleName(resp.Data[i].VMName) == name {
			return &resp.Data[i], nil
		}
	}
	return nil, nil
}

func vmExists(ctx context.Context, c *client.Client, name string) (bool, error) {
	vm, err := findVM(ctx, c, name)
	return vm != nil, err
}

func vmCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Get("name").(string)

	if existing, err := findVM(ctx, c, name); err != nil {
		return diag.FromErr(err)
	} else if existing != nil {
		return diag.Errorf("a virtual machine named %q already exists; import it with: terraform import <address> %s", name, name)
	}

	payload := map[string]any{
		"vmName":         name,
		"regions":        regionsFor(d, c),
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

	if err := c.Do(ctx, http.MethodPost, "/api/VirtualMachine/create-vm", payload, nil); err != nil {
		return diag.FromErr(err)
	}
	// Record the ID before waiting so a timeout still leaves the VM tracked
	// (Terraform marks it tainted) instead of orphaning it.
	d.SetId(name)
	_ = d.Set("imported", false)

	if err := waitForPresence(ctx, c, name, vmExists, true, d.Timeout(schema.TimeoutCreate)); err != nil {
		return diag.FromErr(err)
	}
	return vmRead(ctx, d, meta)
}

func vmRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	vm, err := findVM(ctx, c, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	if vm == nil {
		// Gone outside Terraform: drop it from state rather than failing the plan.
		d.SetId("")
		return nil
	}
	_ = d.Set("name", d.Id())
	_ = d.Set("status", vm.Status)
	_ = d.Set("private_ip", vm.PrivateIp)
	if vm.RegionId != "" {
		_ = d.Set("region", vm.RegionId)
	}
	return nil
}

func vmDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Id()

	err := c.Do(ctx, http.MethodDelete, "/api/VirtualMachine/destroy-vm"+client.Query("vmName", name), nil, nil)
	if err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	if err := waitForPresence(ctx, c, name, vmExists, false, d.Timeout(schema.TimeoutDelete)); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
