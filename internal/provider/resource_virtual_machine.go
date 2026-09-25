package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"golang.org/x/crypto/ssh"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

const defaultVMImage = "ubuntu-24.04-amd64"

func resourceVirtualMachine() *schema.Resource {
	return &schema.Resource{
		Description: "A KVM virtual machine. The HIOK API has no in-place update for virtual machines, " +
			"so changing any setting other than `timeouts` destroys the VM (including its disk) and creates a new one.",
		CreateContext: vmCreate,
		ReadContext:   vmRead,
		UpdateContext: vmUpdate,
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
				Description: "Generate an ed25519 keypair for the cloud-init user. The private key is returned in " +
					"`private_key_openssh` (stored in Terraform state, so protect your state).",
			},
			"private_key_openssh": {
				Type:        schema.TypeString,
				Computed:    true,
				Sensitive:   true,
				Description: "OpenSSH private key generated when `generate_ssh_key` is true. Save it with `terraform output -raw`.",
			},
			"public_key_openssh": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Public half of the generated key, in authorized_keys format.",
			},
			"power_state": {
				Type:         schema.TypeString,
				Optional:     true,
				Computed:     true,
				ValidateFunc: validation.StringInSlice([]string{"running", "stopped"}, false),
				Description:  "Desired power state, `running` or `stopped`. Changed in place (start/stop), without replacing the VM.",
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
				Description: "Address on the attached network, e.g. `10.20.0.2`.",
			},
			"public_ip": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "First public address attached to the VM, if any.",
			},
			"ssh_command": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "Command the platform suggests for connecting, e.g. `ssh -p 22000 ubuntu@web-01-canada-1a2b3c.hiokcloud.com`.",
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
		// The platform's own generateSshKey keeps no retrievable key (its
		// ssh-key endpoint answers 404), so generate the pair here.
		priv, pub, err := generateSSHKey(name)
		if err != nil {
			return diag.FromErr(err)
		}
		payload["sshPublicKey"] = pub
		payload["authType"] = "ssh"
		_ = d.Set("private_key_openssh", priv)
		_ = d.Set("public_key_openssh", pub)
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
	if d.Get("power_state").(string) == "stopped" {
		if err := setPower(ctx, c, d, "stopped", d.Timeout(schema.TimeoutCreate)); err != nil {
			return diag.FromErr(err)
		}
	}
	return vmRead(ctx, d, meta)
}

func vmUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if d.HasChange("power_state") {
		if want := d.Get("power_state").(string); want != "" {
			if err := setPower(ctx, c, d, want, 10*time.Minute); err != nil {
				return diag.FromErr(err)
			}
		}
	}
	return recordOnly(vmRead)(ctx, d, meta)
}

// setPower starts or stops the VM and waits for list-vms-info to agree.
func setPower(ctx context.Context, c *client.Client, d *schema.ResourceData, want string, timeout time.Duration) error {
	action := "start-vm"
	if want == "stopped" {
		action = "stop-vm"
	}
	body := map[string]any{"vmName": d.Id()}
	if r := d.Get("region").(string); r != "" {
		body["regions"] = []string{r}
	}
	if err := c.DoIdempotent(ctx, http.MethodPost, "/api/VirtualMachine/"+action, body, nil); err != nil {
		return err
	}
	polls := 0
	isWanted := func(ctx context.Context, c *client.Client, name string) (bool, error) {
		vm, err := findVM(ctx, c, name)
		if err != nil || vm == nil {
			return false, err
		}
		if powerState(vm.state()) == want {
			return true, nil
		}
		// A platform restart can undo the request; re-send it now and then.
		if polls++; polls%12 == 0 {
			_ = c.DoIdempotent(ctx, http.MethodPost, "/api/VirtualMachine/"+action, body, nil)
		}
		return false, nil
	}
	if err := waitForPresence(ctx, c, d.Id(), isWanted, true, timeout); err != nil {
		return fmt.Errorf("waiting for %q to be %s: %w", d.Id(), want, err)
	}
	return nil
}

// powerState maps libvirt states to running/stopped.
func powerState(state string) string {
	switch strings.ToLower(state) {
	case "running", "idle", "blocked":
		return "running"
	case "shutoff", "shutdown", "stopped", "crashed", "paused", "pmsuspended":
		return "stopped"
	}
	return strings.ToLower(state)
}

// generateSSHKey returns an OpenSSH private key and authorized_keys line.
func generateSSHKey(comment string) (private, public string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment, nil
}

// vmConnection is GET /api/VirtualMachine/{vmName}/connect.
type vmConnection struct {
	PrivateIp  string `json:"privateIp"`
	Hostname   string `json:"hostname"`
	SSHCommand string `json:"sshCommand"`
	PublicIps  []struct {
		Address string `json:"address"`
	} `json:"publicIps"`
}

func vmConnect(ctx context.Context, c *client.Client, name string) (*vmConnection, error) {
	var resp struct {
		Data vmConnection `json:"data"`
	}
	err := c.Do(ctx, http.MethodGet, "/api/VirtualMachine/"+url.PathEscape(name)+"/connect", nil, &resp)
	return &resp.Data, err
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
	_ = d.Set("power_state", powerState(vm.state()))
	if vm.PrivateIp != "" {
		_ = d.Set("private_ip", vm.PrivateIp)
	}
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
	// Addresses and connection details come from a second endpoint. They are
	// informational, so a failure here must not break plans.
	if conn, err := vmConnect(ctx, c, d.Id()); err == nil {
		if conn.PrivateIp != "" {
			_ = d.Set("private_ip", conn.PrivateIp)
		}
		public := ""
		if len(conn.PublicIps) > 0 {
			public = conn.PublicIps[0].Address
		}
		_ = d.Set("public_ip", public)
		_ = d.Set("ssh_command", conn.SSHCommand)
		if conn.Hostname != "" {
			_ = d.Set("hostname", conn.Hostname)
		}
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
