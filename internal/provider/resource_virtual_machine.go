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

const defaultVMImage = "ubuntu-24.04-amd64"

func resourceVirtualMachine() *schema.Resource {
	return &schema.Resource{
		Description: "A KVM virtual machine. The HIOK API has no in-place update for virtual machines, " +
			"so changing any setting other than `timeouts` destroys the VM (including its disk) and creates a new one.",
		CreateContext: vmCreate,
		ReadContext:   vmRead,
		UpdateContext: recordOnly(vmRead),
		DeleteContext: vmDelete,
		Importer:      &schema.ResourceImporter{StateContext: importState},
		CustomizeDiff: createTimeOnly("image", "vcpu_count", "ram_gb", "disk_size_gb", "network_name", "username", "ssh_public_key", "generate_ssh_key", "password"),

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
				Description: "Region to deploy into, e.g. `canada`. Defaults to the provider's region.",
			},
			"image": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      defaultVMImage,
				ValidateFunc: validation.StringIsNotWhiteSpace,
				Description:  "Base image ID, as listed in `hiok_vm_images.ids` (e.g. `ubuntu-24.04-amd64`, `debian-12-amd64`).",
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
			"disk_size_gb": {
				Type:         schema.TypeInt,
				Optional:     true,
				ValidateFunc: validation.IntBetween(1, 65536),
				Description:  "Root disk size in GiB. Defaults to the platform's size for the image.",
			},
			"network_name": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      "default",
				ValidateFunc: validation.StringIsNotWhiteSpace,
				Description:  "Virtual network to attach to, usually a `hiok_virtual_network` name.",
			},
			"username": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "cloud-init user created on first boot.",
			},
			"ssh_public_key": {
				Type:          schema.TypeString,
				Optional:      true,
				ConflictsWith: []string{"generate_ssh_key", "password"},
				Description:   "Public key authorised for the cloud-init user.",
			},
			"generate_ssh_key": {
				Type:          schema.TypeBool,
				Optional:      true,
				ConflictsWith: []string{"ssh_public_key", "password"},
				Description:   "Have the platform generate a keypair; download the private key from the HIOK console.",
			},
			"password": {
				Type:          schema.TypeString,
				Optional:      true,
				Sensitive:     true,
				ConflictsWith: []string{"ssh_public_key", "generate_ssh_key"},
				Description:   "Password for the cloud-init user (password authentication). Prefer SSH keys.",
			},
			"vm_id": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Platform ID of the virtual machine.",
			},
			"hostname": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Public DNS name assigned at creation, e.g. `web-01-canada-1a2b3c.hiokcloud.com`. Not known for imported VMs.",
			},
			"status": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Current power state, e.g. `running`.",
			},
			"private_ip": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Address on the attached network, when the API reports one (the current API does not).",
			},
			"imported": importedSchema(),
		},
	}
}

// vmInfo is one entry of GET /api/VirtualMachine/list-vms-info. Older API
// versions used vmName/status; both spellings are accepted.
type vmInfo struct {
	Name      string `json:"name"`
	VMName    string `json:"vmName"`
	State     string `json:"state"`
	Status    string `json:"status"`
	ID        string `json:"id"`
	VCPU      int    `json:"vCpu"`
	RegionId  string `json:"regionId"`
	PrivateIp string `json:"privateIp"`
}

func (v *vmInfo) visibleName() string { return client.VisibleName(firstNonEmpty(v.Name, v.VMName)) }
func (v *vmInfo) state() string       { return firstNonEmpty(v.State, v.Status) }

func findVM(ctx context.Context, c *client.Client, name string) (*vmInfo, error) {
	var resp struct {
		Data []vmInfo `json:"data"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/VirtualMachine/list-vms-info", nil, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		if resp.Data[i].visibleName() == name {
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

	region, err := resolveRegion(ctx, d, c)
	if err != nil {
		return diag.FromErr(err)
	}
	if existing, err := findVM(ctx, c, name); err != nil {
		return diag.FromErr(err)
	} else if existing != nil {
		return diag.Errorf("a virtual machine named %q already exists; import it with: terraform import <address> %s", name, name)
	}

	payload := map[string]any{
		"vmName":         name,
		"regions":        []string{region},
		"sourceFilePath": d.Get("image").(string),
		"vcpuCount":      d.Get("vcpu_count").(int),
		"ramSize":        d.Get("ram_gb").(float64),
		"networkName":    d.Get("network_name").(string),
	}
	if v, ok := d.GetOk("disk_size_gb"); ok {
		payload["diskSizeGb"] = v.(int)
	}
	if v, ok := d.GetOk("username"); ok {
		payload["username"] = v.(string)
	}
	switch {
	case d.Get("ssh_public_key").(string) != "":
		payload["sshPublicKey"] = d.Get("ssh_public_key").(string)
		payload["authType"] = "ssh"
	case d.Get("generate_ssh_key").(bool):
		payload["generateSshKey"] = true
		payload["authType"] = "ssh"
	case d.Get("password").(string) != "":
		payload["password"] = d.Get("password").(string)
		payload["authType"] = "password"
	}

	// The API answers 202 "Deployment Started" and provisions asynchronously.
	var resp struct {
		Data struct {
			Hostname string `json:"hostname"`
		} `json:"data"`
	}
	if err := c.Do(ctx, http.MethodPost, "/api/VirtualMachine/create-vm", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	// Record the ID before waiting so a timeout still leaves the VM tracked
	// (Terraform marks it tainted) instead of orphaning it.
	d.SetId(name)
	_ = d.Set("imported", false)
	_ = d.Set("region", region)
	_ = d.Set("hostname", resp.Data.Hostname)

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
	_ = d.Set("status", vm.state())
	_ = d.Set("private_ip", vm.PrivateIp)
	if vm.ID != "" {
		_ = d.Set("vm_id", vm.ID)
	}
	if vm.RegionId != "" {
		_ = d.Set("region", vm.RegionId)
	}
	// Fill vcpu_count after import only, so the API can never force a replacement.
	if vm.VCPU > 0 && d.Get("imported").(bool) {
		_ = d.Set("vcpu_count", vm.VCPU)
	}
	return nil
}

func vmDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Id()

	// destroy-vm takes a JSON body; a query string is rejected with 415.
	body := map[string]any{"vmName": name}
	if r := d.Get("region").(string); r != "" {
		body["regions"] = []string{r}
	}
	err := c.Do(ctx, http.MethodDelete, "/api/VirtualMachine/destroy-vm", body, nil)
	if err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	// The API answers 200 even for a VM that does not exist, so confirm.
	if err := waitForPresence(ctx, c, name, vmExists, false, d.Timeout(schema.TimeoutDelete)); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
