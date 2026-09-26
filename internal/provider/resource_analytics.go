package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// resourceAnalyticsCluster manages a HIOK Analytics cluster — managed columnar
// analytics, powered by ClickHouse.
//
// Sizing, storage and virtual-network attachment are all ForceNew: the service
// provisions a container with fixed limits and, where requested, an interface on
// the customer's bridge, so changing any of them means a new cluster rather than
// an in-place edit.
func resourceAnalyticsCluster() *schema.Resource {
	return &schema.Resource{
		Description:   "A managed columnar analytics cluster for event, log and time-series data.",
		CreateContext: analyticsClusterCreate,
		ReadContext:   analyticsClusterRead,
		DeleteContext: analyticsClusterDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"cluster_name": {Type: schema.TypeString, Required: true, ForceNew: true},
			"region":       {Type: schema.TypeString, Optional: true, ForceNew: true},
			"sku": {
				Type: schema.TypeString, Optional: true, ForceNew: true, Default: "small",
				Description: "dev (1 vCPU / 2 GiB), small (2/8), medium (4/16) or large (8/32).",
			},
			"engine_version": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "24.8-alpine"},
			"database_name":  {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "default"},
			"admin_username": {Type: schema.TypeString, Optional: true, ForceNew: true, Default: "hiokadmin"},
			"storage_gb":     {Type: schema.TypeInt, Optional: true, ForceNew: true, Default: 100},
			"vnet_name": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Virtual network to join, so machines on it reach the cluster privately.",
			},
			"vnet_address": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Address to take on that network, in CIDR form, e.g. 10.0.0.20/24.",
			},

			"status":        {Type: schema.TypeString, Computed: true},
			"host":          {Type: schema.TypeString, Computed: true},
			"http_port":     {Type: schema.TypeInt, Computed: true},
			"native_port":   {Type: schema.TypeInt, Computed: true},
			"http_endpoint": {Type: schema.TypeString, Computed: true},
			"vnet_attached": {Type: schema.TypeBool, Computed: true},
			"v_cpu":         {Type: schema.TypeInt, Computed: true},
			"ram_gb":        {Type: schema.TypeFloat, Computed: true},
		},
	}
}

func analyticsClusterCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"clusterName":   d.Get("cluster_name").(string),
		"sku":           d.Get("sku").(string),
		"engineVersion": d.Get("engine_version").(string),
		"databaseName":  d.Get("database_name").(string),
		"adminUsername": d.Get("admin_username").(string),
		"storageGb":     d.Get("storage_gb").(int),
	}
	if r := regionOrDefault(d, c); r != "" {
		payload["region"] = r
	}
	if v, ok := d.GetOk("vnet_name"); ok {
		payload["vnetName"] = v.(string)
	}
	if v, ok := d.GetOk("vnet_address"); ok {
		payload["vnetAddress"] = v.(string)
	}

	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := mergeExtras(d, payload, "hiok_analytics_cluster"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/Analytics", payload, &out); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(out.Data.ID)
	return analyticsClusterRead(ctx, d, meta)
}

func analyticsClusterRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var out struct {
		Data struct {
			ClusterName  string  `json:"clusterName"`
			Status       string  `json:"status"`
			Region       string  `json:"region"`
			Host         string  `json:"host"`
			HTTPPort     int     `json:"httpPort"`
			NativePort   int     `json:"nativePort"`
			HTTPEndpoint string  `json:"httpEndpoint"`
			VNetAttached bool    `json:"vNetAttached"`
			VCpu         int     `json:"vCpu"`
			RamGb        float64 `json:"ramGb"`
		} `json:"data"`
	}
	// A cluster deleted outside Terraform should drop out of state rather than
	// failing every subsequent plan.
	if err := c.Do(context.Background(), "GET", "/api/Analytics/"+d.Id(), nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	_ = d.Set("cluster_name", out.Data.ClusterName)
	_ = d.Set("status", out.Data.Status)
	_ = d.Set("region", out.Data.Region)
	_ = d.Set("host", out.Data.Host)
	_ = d.Set("http_port", out.Data.HTTPPort)
	_ = d.Set("native_port", out.Data.NativePort)
	_ = d.Set("http_endpoint", out.Data.HTTPEndpoint)
	_ = d.Set("vnet_attached", out.Data.VNetAttached)
	_ = d.Set("v_cpu", out.Data.VCpu)
	_ = d.Set("ram_gb", out.Data.RamGb)
	return nil
}

func analyticsClusterDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/Analytics/"+d.Id(), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// resourceStreamingEndpoint manages a live streaming endpoint: a named path on a
// region's media edge with a generated publish key.
func resourceStreamingEndpoint() *schema.Resource {
	return &schema.Resource{
		Description:   "A live streaming endpoint with RTSP/RTMP/WebRTC/SRT ingest and HLS/WebRTC playback.",
		CreateContext: streamingEndpointCreate,
		ReadContext:   streamingEndpointRead,
		DeleteContext: streamingEndpointDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name":   {Type: schema.TypeString, Required: true, ForceNew: true},
			"region": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"kind": {
				Type: schema.TypeString, Optional: true, ForceNew: true, Default: "camera",
				Description: "camera, video or content.",
			},

			"path": {Type: schema.TypeString, Computed: true, Description: "URL path the stream is published and played on."},
			"publish_key": {
				Type: schema.TypeString, Computed: true, Sensitive: true,
				Description: "Anyone holding this can publish to the stream.",
			},
			"status":       {Type: schema.TypeString, Computed: true},
			"rtmp_ingest":  {Type: schema.TypeString, Computed: true, Sensitive: true},
			"rtsp_ingest":  {Type: schema.TypeString, Computed: true, Sensitive: true},
			"srt_ingest":   {Type: schema.TypeString, Computed: true, Sensitive: true},
			"hls_playback": {Type: schema.TypeString, Computed: true},
			"whep_playback": {
				Type: schema.TypeString, Computed: true,
				Description: "WebRTC playback (WHEP) endpoint.",
			},
		},
	}
}

type streamingEndpointPayload struct {
	Data struct {
		Endpoint struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Path       string `json:"path"`
			PublishKey string `json:"publishKey"`
			Region     string `json:"region"`
			Kind       string `json:"kind"`
			Status     string `json:"status"`
		} `json:"endpoint"`
		URLs struct {
			Ingest struct {
				RTSP string `json:"rtsp"`
				RTMP string `json:"rtmp"`
				SRT  string `json:"srt"`
			} `json:"ingest"`
			Playback struct {
				HLS  string `json:"hls"`
				WHEP string `json:"webrtcWhep"`
			} `json:"playback"`
		} `json:"urls"`
	} `json:"data"`
}

func (p *streamingEndpointPayload) apply(d *schema.ResourceData) {
	_ = d.Set("name", p.Data.Endpoint.Name)
	_ = d.Set("path", p.Data.Endpoint.Path)
	_ = d.Set("publish_key", p.Data.Endpoint.PublishKey)
	_ = d.Set("region", p.Data.Endpoint.Region)
	_ = d.Set("kind", p.Data.Endpoint.Kind)
	_ = d.Set("status", p.Data.Endpoint.Status)
	_ = d.Set("rtsp_ingest", p.Data.URLs.Ingest.RTSP)
	_ = d.Set("rtmp_ingest", p.Data.URLs.Ingest.RTMP)
	_ = d.Set("srt_ingest", p.Data.URLs.Ingest.SRT)
	_ = d.Set("hls_playback", p.Data.URLs.Playback.HLS)
	_ = d.Set("whep_playback", p.Data.URLs.Playback.WHEP)
}

func streamingEndpointCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	payload := map[string]any{
		"name": d.Get("name").(string),
		"kind": d.Get("kind").(string),
	}
	if r := regionOrDefault(d, c); r != "" {
		payload["region"] = r
	}

	var out streamingEndpointPayload
	if err := c.Do(context.Background(), "POST", "/api/streaming", payload, &out); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(out.Data.Endpoint.ID)
	out.apply(d)
	return nil
}

func streamingEndpointRead(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	var out streamingEndpointPayload
	if err := c.Do(context.Background(), "GET", "/api/streaming/"+d.Id(), nil, &out); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	out.apply(d)
	return nil
}

func streamingEndpointDelete(_ context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/streaming/"+d.Id(), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
