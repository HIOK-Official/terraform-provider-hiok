package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

func resourceContainer() *schema.Resource {
	return &schema.Resource{
		Description:   "A container instance.",
		CreateContext: containerCreate,
		ReadContext:   containerRead,
		DeleteContext: containerDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":     {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Container name."},
			"image":    {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Image reference, e.g. nginx:alpine."},
			"region":   {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Region to deploy into."},
			"hostname": {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Hostname inside the container."},
			"env": {
				Type:        schema.TypeList,
				Optional:    true,
				ForceNew:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: `Environment variables as "KEY=value" strings.`,
			},
			"command": {
				Type:        schema.TypeList,
				Optional:    true,
				ForceNew:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Override the image entrypoint command.",
			},
			"status": {Type: schema.TypeString, Computed: true, Description: "Current container state."},
		},
	}
}

func containerRegions(d *schema.ResourceData, c *client.Client) []string {
	if r, ok := d.GetOk("region"); ok && r.(string) != "" {
		return []string{r.(string)}
	}
	return c.Regions
}

func stringList(d *schema.ResourceData, key string) []string {
	raw := d.Get(key).([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func containerCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	payload := map[string]any{
		"name":    d.Get("name").(string),
		"image":   d.Get("image").(string),
		"regions": containerRegions(d, c),
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

	if err := c.Do("POST", "/api/Containers/createcontainer", payload, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(d.Get("name").(string))
	return containerRead(ctx, d, meta)
}

func containerRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"data"`
	}
	body := map[string]any{"pageNumber": 1, "pageSize": 200}
	if err := c.Do("POST", "/api/Containers/listallcontainers", body, &resp); err != nil {
		return diag.FromErr(err)
	}

	for _, item := range resp.Data {
		name := item.Name
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}
		if name == d.Id() {
			_ = d.Set("name", name)
			_ = d.Set("status", item.Status)
			return nil
		}
	}

	d.SetId("")
	return nil
}

func containerDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	body := map[string]any{"containerName": d.Id(), "force": true}
	if err := c.Do("POST", "/api/Containers/deletecontainer", body, nil); err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
