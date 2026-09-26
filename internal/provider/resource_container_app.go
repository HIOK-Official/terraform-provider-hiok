package provider

import (
	"context"
	"net/url"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// resourceContainerAppEnvironment is the network boundary apps share. Apps inside one
// reach each other by app name; anything outside cannot reach them at all.
func resourceContainerAppEnvironment() *schema.Resource {
	return &schema.Resource{
		Description: "A Container App Environment — the shared network apps inside it use to " +
			"address each other by name, and the boundary that keeps everything else out.",
		CreateContext: containerAppEnvironmentCreate,
		ReadContext:   containerAppEnvironmentRead,
		DeleteContext: containerAppEnvironmentDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":   {Type: schema.TypeString, Required: true, ForceNew: true},
			"region": {Type: schema.TypeString, Optional: true, ForceNew: true},

			"status":       {Type: schema.TypeString, Computed: true},
			"network_name": {Type: schema.TypeString, Computed: true, Description: "Docker network backing the environment."},
		},
	}
}

type containerAppEnvironment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Region      string `json:"region"`
	Status      string `json:"status"`
	NetworkName string `json:"networkName"`
}

func containerAppEnvironmentCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	region := d.Get("region").(string)
	if region == "" && len(c.Regions) > 0 {
		region = c.Regions[0]
	}

	payload := map[string]any{"name": d.Get("name").(string), "region": region}
	if err := mergeExtras(d, payload, "hiok_container_app_environment"); err != nil {
		return diag.FromErr(err)
	}
	var env containerAppEnvironment
	if err := c.Do(context.Background(), "POST", "/api/ContainerApp/environments", payload, &env); err != nil {
		return diag.FromErr(err)
	}
	if env.ID == "" {
		return diag.Errorf("the API accepted the request but returned no environment")
	}

	d.SetId(env.ID)
	_ = d.Set("region", env.Region)
	_ = d.Set("status", env.Status)
	_ = d.Set("network_name", env.NetworkName)
	return nil
}

func containerAppEnvironmentRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	// The API lists environments rather than serving one by id, so the read filters.
	// This endpoint wraps its list in {message, data}, unlike the apps endpoint.
	var resp struct {
		Data []containerAppEnvironment `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", "/api/ContainerApp/environments", nil, &resp); err != nil {
		return diag.FromErr(err)
	}
	for _, env := range resp.Data {
		if env.ID == d.Id() {
			_ = d.Set("name", env.Name)
			_ = d.Set("region", env.Region)
			_ = d.Set("status", env.Status)
			_ = d.Set("network_name", env.NetworkName)
			return nil
		}
	}
	d.SetId("")
	return nil
}

func containerAppEnvironmentDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/ContainerApp/environments/"+url.PathEscape(d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// resourceContainerApp runs a container image as an addressable app.
func resourceContainerApp() *schema.Resource {
	return &schema.Resource{
		Description:   "A Container App: an image run as a named, addressable service.",
		CreateContext: containerAppCreate,
		ReadContext:   containerAppRead,
		DeleteContext: containerAppDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":   {Type: schema.TypeString, Required: true, ForceNew: true},
			"image":  {Type: schema.TypeString, Required: true, ForceNew: true},
			"region": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"environment_id": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Environment to join. Apps sharing one address each other by name.",
			},
			"target_port":  {Type: schema.TypeInt, Optional: true, Default: 80, ForceNew: true},
			"vcpu":         {Type: schema.TypeFloat, Optional: true, Default: 0.5, ForceNew: true},
			"ram_gb":       {Type: schema.TypeFloat, Optional: true, Default: 1.0, ForceNew: true},
			"min_replicas": {Type: schema.TypeInt, Optional: true, Default: 1, ForceNew: true},
			"max_replicas": {Type: schema.TypeInt, Optional: true, Default: 1, ForceNew: true},
			"external_ingress": {
				Type: schema.TypeBool, Optional: true, Default: true, ForceNew: true,
				Description: "Publish a host port. Leave false and the app is reachable only " +
					"from inside its environment — no port is published at all.",
			},
			"environment_variables": {
				Type: schema.TypeMap, Optional: true, ForceNew: true,
				Elem: &schema.Schema{Type: schema.TypeString},
			},

			"status": {Type: schema.TypeString, Computed: true},
			"host_port": {
				Type: schema.TypeInt, Computed: true,
				Description: "Published port, or 0 for an app without external ingress.",
			},
			"default_hostname": {Type: schema.TypeString, Computed: true},
		},
	}
}

type containerApp struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Image           string `json:"image"`
	Region          string `json:"region"`
	Status          string `json:"status"`
	TargetPort      int    `json:"targetPort"`
	HostPort        int    `json:"hostPort"`
	ExternalIngress bool   `json:"externalIngress"`
	DefaultHostname string `json:"defaultHostname"`
}

func containerAppApply(d *schema.ResourceData, a *containerApp) {
	_ = d.Set("name", a.Name)
	_ = d.Set("image", a.Image)
	_ = d.Set("region", a.Region)
	_ = d.Set("status", a.Status)
	_ = d.Set("target_port", a.TargetPort)
	_ = d.Set("host_port", a.HostPort)
	_ = d.Set("external_ingress", a.ExternalIngress)
	_ = d.Set("default_hostname", a.DefaultHostname)
}

func containerAppCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	region := d.Get("region").(string)
	if region == "" && len(c.Regions) > 0 {
		region = c.Regions[0]
	}

	payload := map[string]any{
		"name":            d.Get("name").(string),
		"image":           d.Get("image").(string),
		"region":          region,
		"targetPort":      d.Get("target_port").(int),
		"vCpu":            d.Get("vcpu").(float64),
		"ramGb":           d.Get("ram_gb").(float64),
		"minReplicas":     d.Get("min_replicas").(int),
		"maxReplicas":     d.Get("max_replicas").(int),
		"externalIngress": d.Get("external_ingress").(bool),
	}
	if v, ok := d.GetOk("environment_id"); ok && v.(string) != "" {
		payload["environmentId"] = v.(string)
	}
	if v, ok := d.GetOk("environment_variables"); ok {
		env := map[string]string{}
		for key, val := range v.(map[string]any) {
			env[key] = val.(string)
		}
		if len(env) > 0 {
			payload["environment"] = env
		}
	}

	var app containerApp
	if err := mergeExtras(d, payload, "hiok_container_app"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/ContainerApp", payload, &app); err != nil {
		return diag.FromErr(err)
	}
	if app.ID == "" {
		return diag.Errorf("the API accepted the request but returned no app")
	}

	d.SetId(app.ID)
	containerAppApply(d, &app)
	return nil
}

func containerAppRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var app containerApp
	if err := c.Do(context.Background(), "GET", "/api/ContainerApp/"+url.PathEscape(d.Id()), nil, &app); err != nil || app.ID == "" {
		if err != nil && !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	containerAppApply(d, &app)
	return nil
}

func containerAppDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/ContainerApp/"+url.PathEscape(d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
