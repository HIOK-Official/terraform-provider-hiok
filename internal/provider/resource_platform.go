package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// The rest of the platform, as Terraform resources.
//
// The provider covered a little over half of what the console can create, so
// anybody managing HIOK as code had to click the remainder into existence and
// then remember it was not in their state. These are the kinds that were
// missing — a Kubernetes cluster, a registry, a cache, the two relational
// engines besides Postgres, a stream job — plus the ones that only exist because
// of work done since: a live stream's destinations, a compose stack, a swarm
// service.

// ── helpers ─────────────────────────────────────────────────────────────────

// regionOf is the region a resource names, or the provider's default.
func regionOf(d *schema.ResourceData, c *client.Client) string {
	if r, ok := d.GetOk("region"); ok && r.(string) != "" {
		return r.(string)
	}
	if len(c.Regions) > 0 {
		return c.Regions[0]
	}
	return "canada"
}

// idFromResponse reads the id out of the platform's standard {data:{id}} reply.
func idFromResponse(raw map[string]any) string {
	data, ok := raw["data"].(map[string]any)
	if !ok {
		// Some endpoints answer with the object itself rather than wrapping it.
		data = raw
	}
	for _, key := range []string{"id", "Id", "ID"} {
		if v, ok := data[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// optionalString copies a value into the payload only when it was set, so a
// field the customer left alone keeps whatever default the API applies rather
// than being overwritten with an empty string.
func optionalString(d *schema.ResourceData, key, field string, payload map[string]any) {
	if v, ok := d.GetOk(key); ok && v.(string) != "" {
		payload[field] = v.(string)
	}
}

// deleteByID is the shape almost every delete takes here.
func deleteByID(c *client.Client, path string, d *schema.ResourceData) diag.Diagnostics {
	if err := c.Do(context.Background(), "DELETE", fmt.Sprintf("%s/%s", path, d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── Kubernetes ──────────────────────────────────────────────────────────────

func resourceKubernetesCluster() *schema.Resource {
	return &schema.Resource{
		Description:   "A managed Kubernetes cluster.",
		CreateContext: kubernetesClusterCreate,
		ReadContext:   kubernetesClusterRead,
		DeleteContext: func(_ context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
			return deleteByID(m.(*client.Client), "/api/kubernetes/clusters", d)
		},
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":           {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Cluster name."},
			"region":         {Type: schema.TypeString, Optional: true, ForceNew: true},
			"version":        {Type: schema.TypeString, Optional: true, ForceNew: true, Description: "Kubernetes version. Defaults to the region's current."},
			"node_count":     {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 1, Description: "Worker nodes."},
			"node_cpus":      {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 2, Description: "vCPU per node."},
			"node_memory_mb": {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 2048, Description: "Memory per node, in MiB."},
			"status":         {Type: schema.TypeString, Computed: true},
		},
	}
}

func kubernetesClusterCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"name":         d.Get("name").(string),
		"region":       regionOf(d, c),
		"nodeCount":    d.Get("node_count").(int),
		"nodeCpus":     d.Get("node_cpus").(int),
		"nodeMemoryMb": d.Get("node_memory_mb").(int),
	}
	optionalString(d, "version", "version", payload)

	var resp map[string]any
	if err := mergeExtras(d, payload, "hiok_kubernetes_cluster"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/kubernetes/clusters", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(idFromResponse(resp))
	return kubernetesClusterRead(ctx, d, meta)
}

func kubernetesClusterRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var resp map[string]any
	if err := c.Do(context.Background(), "GET", "/api/kubernetes/clusters/"+d.Id(), nil, &resp); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		// A cluster deleted outside Terraform is not an error; it is drift, and
		// clearing the id is how a plan comes to recreate it.
		d.SetId("")
		return nil
	}
	if data, ok := resp["data"].(map[string]any); ok {
		if status, ok := data["status"].(string); ok {
			_ = d.Set("status", status)
		}
	}
	return nil
}

// ── Container registry ──────────────────────────────────────────────────────

func resourceContainerRegistry() *schema.Resource {
	return &schema.Resource{
		Description:   "A private container registry.",
		CreateContext: containerRegistryCreate,
		ReadContext:   schema.NoopContext,
		DeleteContext: func(_ context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
			return deleteByID(m.(*client.Client), "/api/container-registry", d)
		},
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":           {Type: schema.TypeString, Required: true, ForceNew: true},
			"region":         {Type: schema.TypeString, Optional: true, ForceNew: true},
			"sku":            {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "Basic", Description: "Basic, Standard or Premium."},
			"storage_gb":     {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 10},
			"anonymous_pull": {Type: schema.TypeBool, Optional: true, ForceNew: true},
			"network_access": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "public"},
			"login_server":   {Type: schema.TypeString, Computed: true},
		},
	}
}

func containerRegistryCreate(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"registryName":  d.Get("name").(string),
		"region":        regionOf(d, c),
		"sku":           d.Get("sku").(string),
		"storageGb":     d.Get("storage_gb").(int),
		"anonymousPull": d.Get("anonymous_pull").(bool),
		"networkAccess": d.Get("network_access").(string),
	}

	var resp map[string]any
	if err := mergeExtras(d, payload, "hiok_container_registry"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/container-registry", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(idFromResponse(resp))
	if data, ok := resp["data"].(map[string]any); ok {
		if server, ok := data["loginServer"].(string); ok {
			_ = d.Set("login_server", server)
		}
	}
	return nil
}

// ── Cache ───────────────────────────────────────────────────────────────────

func resourceCache() *schema.Resource {
	return &schema.Resource{
		Description:   "A managed in-memory cache.",
		CreateContext: cacheCreate,
		ReadContext:   schema.NoopContext,
		DeleteContext: func(_ context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
			return deleteByID(m.(*client.Client), "/api/Cache", d)
		},
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":            {Type: schema.TypeString, Required: true, ForceNew: true},
			"region":          {Type: schema.TypeString, Optional: true, ForceNew: true},
			"engine":          {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "hiok"},
			"tier":            {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "standard"},
			"memory_mb":       {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 512},
			"cpus":            {Type: schema.TypeFloat, Optional: true, ForceNew: true, Default: 1},
			"eviction_policy": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "noeviction"},
			"tls_enabled":     {Type: schema.TypeBool, Optional: true, ForceNew: true, Default: true},
			"public_access":   {Type: schema.TypeBool, Optional: true, ForceNew: true},
		},
	}
}

func cacheCreate(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"name":           d.Get("name").(string),
		"regionId":       regionOf(d, c),
		"engine":         d.Get("engine").(string),
		"tier":           d.Get("tier").(string),
		"memoryMb":       d.Get("memory_mb").(int),
		"cpus":           d.Get("cpus").(float64),
		"evictionPolicy": d.Get("eviction_policy").(string),
		"tlsEnabled":     d.Get("tls_enabled").(bool),
		"publicAccess":   d.Get("public_access").(bool),
	}

	var resp map[string]any
	if err := mergeExtras(d, payload, "hiok_cache"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/Cache", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(idFromResponse(resp))
	return nil
}

// ── Relational databases besides Postgres ───────────────────────────────────

func relationalResource(engine, path, defaultSku string) *schema.Resource {
	return &schema.Resource{
		Description:   fmt.Sprintf("A managed %s server.", engine),
		CreateContext: relationalCreate(path),
		ReadContext:   schema.NoopContext,
		DeleteContext: func(_ context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
			return deleteByID(m.(*client.Client), path, d)
		},
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":           {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Server name."},
			"region":         {Type: schema.TypeString, Optional: true, ForceNew: true},
			"admin_username": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"admin_password": {Type: schema.TypeString, Optional: true, ForceNew: true, Sensitive: true},
			"database_name":  {Type: schema.TypeString, Optional: true, ForceNew: true},
			"engine_version": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"sku":            {Type: schema.TypeString, Optional: true, ForceNew: true, Default: defaultSku},
			"storage_gb":     {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 32},
		},
	}
}

func relationalCreate(path string) schema.CreateContextFunc {
	return func(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
		c := meta.(*client.Client)
		payload := map[string]any{
			"serverName": d.Get("name").(string),
			"region":     regionOf(d, c),
			"sku":        d.Get("sku").(string),
			"storageGb":  d.Get("storage_gb").(int),
		}
		optionalString(d, "admin_username", "adminUsername", payload)
		optionalString(d, "admin_password", "adminPassword", payload)
		optionalString(d, "database_name", "databaseName", payload)
		optionalString(d, "engine_version", "engineVersion", payload)

		var resp map[string]any
		if err := c.Do(context.Background(), "POST", path, payload, &resp); err != nil {
			return diag.FromErr(err)
		}
		d.SetId(idFromResponse(resp))
		return nil
	}
}

// ── Stream analytics ────────────────────────────────────────────────────────

func resourceStreamJob() *schema.Resource {
	return &schema.Resource{
		Description:   "A stream analytics job.",
		CreateContext: streamJobCreate,
		ReadContext:   schema.NoopContext,
		DeleteContext: func(_ context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
			return deleteByID(m.(*client.Client), "/api/StreamAnalytics", d)
		},
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":           {Type: schema.TypeString, Required: true, ForceNew: true},
			"region":         {Type: schema.TypeString, Optional: true, ForceNew: true},
			"engine":         {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "risingwave", Description: "risingwave, flink or spark."},
			"serving_engine": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "none"},
			"sku":            {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "small", Description: "dev, small, medium or large."},
			"storage_gb":     {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 10},
			"parallelism":    {Type: schema.TypeInt, Optional: true, ForceNew: true},
		},
	}
}

func streamJobCreate(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"jobName":       d.Get("name").(string),
		"region":        regionOf(d, c),
		"engine":        d.Get("engine").(string),
		"servingEngine": d.Get("serving_engine").(string),
		"sku":           d.Get("sku").(string),
		"storageGb":     d.Get("storage_gb").(int),
	}
	if v, ok := d.GetOk("parallelism"); ok {
		payload["parallelism"] = v.(int)
	}

	var resp map[string]any
	if err := mergeExtras(d, payload, "hiok_stream_job"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/StreamAnalytics", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(idFromResponse(resp))
	return nil
}

// ── Live stream destinations ────────────────────────────────────────────────

func resourceStreamDestination() *schema.Resource {
	return &schema.Resource{
		Description: "A platform a live stream is repeated to — YouTube, Facebook, an SRT receiver.",
		CreateContext: func(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			c := meta.(*client.Client)
			payload := map[string]any{
				"name":      d.Get("name").(string),
				"platform":  d.Get("platform").(string),
				"targetUrl": d.Get("target_url").(string),
				"mode":      d.Get("mode").(string),
				"enabled":   d.Get("enabled").(bool),
			}
			optionalString(d, "stream_key", "streamKey", payload)
			optionalString(d, "video_bitrate", "videoBitrate", payload)
			optionalString(d, "resolution", "resolution", payload)

			var resp map[string]any
			path := fmt.Sprintf("/api/streaming/%s/destinations", d.Get("streaming_endpoint_id").(string))
			if err := c.Do(context.Background(), "POST", path, payload, &resp); err != nil {
				return diag.FromErr(err)
			}
			d.SetId(idFromResponse(resp))
			return nil
		},
		ReadContext: schema.NoopContext,
		DeleteContext: func(_ context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
			return deleteByID(m.(*client.Client), "/api/streaming/destinations", d)
		},
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"streaming_endpoint_id": {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The stream to repeat."},
			"name":                  {Type: schema.TypeString, Required: true, ForceNew: true},
			"platform":              {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "rtmp", Description: "youtube, facebook, instagram, twitch, boxcast, linkedin, x, kick, rtmp or srt."},
			"target_url":            {Type: schema.TypeString, Required: true, ForceNew: true, Description: "The platform's ingest URL."},
			// A stream key lets its holder broadcast as that channel, so it is
			// sensitive here for the same reason it is encrypted at rest.
			"stream_key":    {Type: schema.TypeString, Optional: true, ForceNew: true, Sensitive: true},
			"mode":          {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "copy", Description: "copy repeats the published bitstream; transcode re-encodes for this destination."},
			"video_bitrate": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"resolution":    {Type: schema.TypeString, Optional: true, ForceNew: true},
			"enabled":       {Type: schema.TypeBool, Optional: true, ForceNew: true, Default: true},
		},
	}
}

// ── Compose stacks ──────────────────────────────────────────────────────────

func resourceDockerStack() *schema.Resource {
	return &schema.Resource{
		Description: "A compose stack: several containers described together in one file.",
		CreateContext: func(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			c := meta.(*client.Client)
			payload := map[string]any{
				"project": d.Get("name").(string),
				"region":  regionOf(d, c),
			}
			optionalString(d, "compose", "content", payload)
			optionalString(d, "storage_path", "storagePath", payload)

			var resp map[string]any
			if err := c.Do(context.Background(), "POST", "/api/Containers/stacks/up", payload, &resp); err != nil {
				return diag.FromErr(err)
			}
			// The project is namespaced by owner on the way in, and that name is what
			// every later operation takes.
			if data, ok := resp["data"].(map[string]any); ok {
				if project, ok := data["project"].(string); ok && project != "" {
					d.SetId(project)
					return nil
				}
			}
			d.SetId(d.Get("name").(string))
			return nil
		},
		ReadContext: schema.NoopContext,
		// remove_volumes_on_destroy only decides what a later destroy does, so
		// changing it needs nothing done now — and it must not force the stack to
		// be rebuilt, which is what marking it ForceNew would mean.
		UpdateContext: schema.NoopContext,
		DeleteContext: func(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			c := meta.(*client.Client)
			body := map[string]any{"project": d.Id(), "removeVolumes": d.Get("remove_volumes_on_destroy").(bool)}
			if err := c.Do(context.Background(), "POST", "/api/Containers/stacks/down", body, nil); err != nil {
				return diag.FromErr(err)
			}
			d.SetId("")
			return nil
		},
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":   {Type: schema.TypeString, Required: true, ForceNew: true, Description: "Stack name."},
			"region": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"compose": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "The compose file itself. Use file() to read one from disk.",
			},
			"storage_path": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: `A compose file already in a storage account, as "account/path/docker-compose.yml".`,
			},
			"remove_volumes_on_destroy": {
				Type: schema.TypeBool, Optional: true, Default: false,
				Description: "Whether destroying the stack also removes its volumes. Off by default: a stack is often rebuilt, and the data is the part nobody wants to lose by accident.",
			},
		},
	}
}

// ── Swarm services ──────────────────────────────────────────────────────────

func resourceSwarmService() *schema.Resource {
	return &schema.Resource{
		Description: "A swarm service: N copies of an image the engine keeps running.",
		CreateContext: func(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			c := meta.(*client.Client)
			payload := map[string]any{
				"name":     d.Get("name").(string),
				"image":    d.Get("image").(string),
				"replicas": d.Get("replicas").(int),
				"cpus":     d.Get("cpus").(float64),
				"memoryMb": d.Get("memory_mb").(int),
				"region":   regionOf(d, c),
				"env":      stringList(d, "env"),
			}
			if port, ok := d.GetOk("published_port"); ok {
				payload["ports"] = []map[string]any{{
					"hostPort":      port.(int),
					"containerPort": d.Get("target_port").(int),
					"protocol":      "tcp",
				}}
			}

			var resp map[string]any
			if err := c.Do(context.Background(), "POST", "/api/Containers/swarm/services", payload, &resp); err != nil {
				return diag.FromErr(err)
			}
			if data, ok := resp["data"].(map[string]any); ok {
				if name, ok := data["name"].(string); ok && name != "" {
					d.SetId(name)
					return nil
				}
			}
			d.SetId(d.Get("name").(string))
			return nil
		},
		ReadContext: schema.NoopContext,
		UpdateContext: func(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
			// Replicas are the one thing a service changes in place — that is what a
			// service is for, and recreating it to scale would drop the traffic.
			c := meta.(*client.Client)
			path := fmt.Sprintf("/api/Containers/swarm/services/%s/scale?replicas=%d", d.Id(), d.Get("replicas").(int))
			if err := c.Do(context.Background(), "POST", path, nil, nil); err != nil {
				return diag.FromErr(err)
			}
			return nil
		},
		DeleteContext: func(_ context.Context, d *schema.ResourceData, m any) diag.Diagnostics {
			return deleteByID(m.(*client.Client), "/api/Containers/swarm/services", d)
		},
		Importer: &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":           {Type: schema.TypeString, Required: true, ForceNew: true},
			"image":          {Type: schema.TypeString, Required: true, ForceNew: true},
			"replicas":       {Type: schema.TypeInt, Optional: true, Default: 1, Description: "How many copies should be running. Changing this scales in place."},
			"region":         {Type: schema.TypeString, Optional: true, ForceNew: true},
			"published_port": {Type: schema.TypeInt, Optional: true, ForceNew: true, Description: "Served by every node in the swarm."},
			"target_port":    {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 80},
			"cpus":           {Type: schema.TypeFloat, Optional: true, ForceNew: true},
			"memory_mb":      {Type: schema.TypeInt, Optional: true, ForceNew: true},
			"env": {
				Type: schema.TypeList, Optional: true, ForceNew: true,
				Elem: &schema.Schema{Type: schema.TypeString}, Description: `Environment as "KEY=value" strings.`,
			},
		},
	}
}
