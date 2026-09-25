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

func resourceContainer() *schema.Resource {
	return &schema.Resource{
		Description:   "A container instance. Changing any setting other than `timeouts` replaces the container.",
		CreateContext: containerCreate,
		ReadContext:   containerRead,
		UpdateContext: recordOnly(containerRead),
		DeleteContext: containerDelete,
		Importer:      &schema.ResourceImporter{StateContext: importState},
		CustomizeDiff: createTimeOnly("image", "region", "hostname", "env", "command"),

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
			Delete: schema.DefaultTimeout(10 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"name":     {Type: schema.TypeString, Required: true, ForceNew: true, ValidateFunc: validateName, Description: "Container name."},
			"image":    {Type: schema.TypeString, Required: true, ValidateFunc: validation.StringIsNotWhiteSpace, Description: "Image reference, e.g. nginx:alpine."},
			"region":   {Type: schema.TypeString, Optional: true, Description: "Region to deploy into. When unset the platform places the container."},
			"hostname": {Type: schema.TypeString, Optional: true, Description: "Hostname inside the container."},
			"env": {
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString, ValidateFunc: validation.StringMatch(envRegexp, `must look like "KEY=value"`)},
				Description: `Environment variables as "KEY=value" strings.`,
			},
			"command": {
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Override the image entrypoint command.",
			},
			"status":       {Type: schema.TypeString, Computed: true, Description: "Current container state, e.g. `running`."},
			"container_id": {Type: schema.TypeString, Computed: true, Description: "Docker container ID."},
			"dns_hostname": {Type: schema.TypeString, Computed: true, Description: "Public DNS name, e.g. `api-01.hiokcloud.com`."},
			"imported":     importedSchema(),
		},
	}
}

type containerInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Image       string `json:"image"`
	DNSHostname string `json:"dnsHostname"`
}

func findContainer(ctx context.Context, c *client.Client, name string) (*containerInfo, error) {
	// listallcontainers ignores paging fields and always returns every
	// container, so one call is enough.
	var resp struct {
		Data []containerInfo `json:"data"`
	}
	body := map[string]any{"pageNumber": 1, "pageSize": 1000}
	if err := c.DoIdempotent(ctx, http.MethodPost, "/api/Containers/listallcontainers", body, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Data {
		if client.VisibleName(resp.Data[i].Name) == name {
			return &resp.Data[i], nil
		}
	}
	return nil, nil
}

func containerExists(ctx context.Context, c *client.Client, name string) (bool, error) {
	ct, err := findContainer(ctx, c, name)
	return ct != nil, err
}

func containerCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Get("name").(string)

	if existing, err := findContainer(ctx, c, name); err != nil {
		return diag.FromErr(err)
	} else if existing != nil {
		return diag.Errorf("a container named %q already exists; import it with: terraform import <address> %s", name, name)
	}

	payload := map[string]any{
		"name":  name,
		"image": d.Get("image").(string),
	}
	if r, ok := d.GetOk("region"); ok {
		if err := c.CheckRegion(ctx, r.(string)); err != nil {
			return diag.FromErr(err)
		}
		payload["regions"] = []string{r.(string)}
	}
	if v, ok := d.GetOk("hostname"); ok {
		payload["hostname"] = v.(string)
	}
	if env := stringList(d, "env"); len(env) > 0 {
		payload["env"] = env
	}
	if cmd := stringList(d, "command"); len(cmd) > 0 {
		payload["cmd"] = cmd
	}

	if err := c.Do(ctx, http.MethodPost, "/api/Containers/createcontainer", payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(name)
	_ = d.Set("imported", false)

	if err := waitForPresence(ctx, c, name, containerExists, true, d.Timeout(schema.TimeoutCreate)); err != nil {
		return diag.FromErr(err)
	}
	return containerRead(ctx, d, meta)
}

func containerRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	ct, err := findContainer(ctx, c, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	if ct == nil {
		d.SetId("")
		return nil
	}
	_ = d.Set("name", d.Id())
	_ = d.Set("status", ct.Status)
	_ = d.Set("container_id", ct.ID)
	_ = d.Set("dns_hostname", ct.DNSHostname)
	if d.Get("imported").(bool) && ct.Image != "" && d.Get("image").(string) == "" {
		_ = d.Set("image", ct.Image)
	}
	return nil
}

func containerDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	name := d.Id()

	body := map[string]any{"containerName": name, "force": true}
	if err := c.Do(ctx, http.MethodPost, "/api/Containers/deletecontainer", body, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	if err := waitForPresence(ctx, c, name, containerExists, false, d.Timeout(schema.TimeoutDelete)); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
