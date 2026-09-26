package provider

import (
	"context"
	"net/url"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// ── MongoDB ─────────────────────────────────────────────────────────────────

// resourceMongoCluster manages a MongoDB deployment. Regions are ForceNew because
// adding a member to a live replica set is a reconfiguration of the set, not a
// property of the cluster record — presenting it as an in-place edit would promise
// something the create path does not do.
func resourceMongoCluster() *schema.Resource {
	return &schema.Resource{
		Description: "A managed MongoDB cluster. One region gives a single node; several give " +
			"a replica set with a member in each.",
		CreateContext: mongoCreate,
		ReadContext:   mongoRead,
		UpdateContext: mongoUpdate,
		DeleteContext: mongoDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"cluster_name": {Type: schema.TypeString, Required: true, ForceNew: true},
			"regions": {
				Type: schema.TypeList, Optional: true, ForceNew: true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "One member per region. The first holds the primary and takes every write.",
			},
			"region": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"consistency": {
				Type: schema.TypeString, Optional: true, Default: "strong",
				Description: "\"strong\", \"session\", \"bounded\" or \"eventual\". This sets the read " +
					"concern, write concern and read preference in the connection string, so it " +
					"governs your drivers. The last two read from secondaries and need more than " +
					"one region.",
			},
			"max_staleness_seconds": {
				Type: schema.TypeInt, Optional: true, Default: 90,
				Description: "Lag ceiling for bounded staleness reads. MongoDB's minimum is 90.",
			},
			"engine_version": {Type: schema.TypeString, Optional: true, Default: "7.0", ForceNew: true},
			"sku":            {Type: schema.TypeString, Optional: true, Default: "small", ForceNew: true},
			"storage_gb":     {Type: schema.TypeInt, Optional: true, Default: 20, ForceNew: true},
			"database_name":  {Type: schema.TypeString, Optional: true, ForceNew: true},
			"vnet_name":      {Type: schema.TypeString, Optional: true, ForceNew: true},

			"status":           {Type: schema.TypeString, Computed: true},
			"status_message":   {Type: schema.TypeString, Computed: true, Description: "Why the cluster is degraded, when it is."},
			"is_replica_set":   {Type: schema.TypeBool, Computed: true},
			"replica_set_name": {Type: schema.TypeString, Computed: true},
			"host":             {Type: schema.TypeString, Computed: true},
			"port":             {Type: schema.TypeInt, Computed: true},
			"read_concern":     {Type: schema.TypeString, Computed: true},
			"write_concern":    {Type: schema.TypeString, Computed: true},
			"read_preference":  {Type: schema.TypeString, Computed: true},
			"connection_string": {
				Type: schema.TypeString, Computed: true, Sensitive: true,
				Description: "Carries the administrator password, so it is marked sensitive.",
			},
		},
	}
}

type mongoCluster struct {
	ID                  string   `json:"id"`
	ClusterName         string   `json:"clusterName"`
	Region              string   `json:"region"`
	Regions             []string `json:"regions"`
	ReplicaSetName      string   `json:"replicaSetName"`
	Consistency         string   `json:"consistency"`
	MaxStalenessSeconds int      `json:"maxStalenessSeconds"`
	ReadConcern         string   `json:"readConcern"`
	WriteConcern        string   `json:"writeConcern"`
	ReadPreference      string   `json:"readPreference"`
	EngineVersion       string   `json:"engineVersion"`
	Sku                 string   `json:"sku"`
	StorageGb           int      `json:"storageGb"`
	Host                string   `json:"host"`
	Port                int      `json:"port"`
	DatabaseName        string   `json:"databaseName"`
	VNetName            string   `json:"vNetName"`
	Status              string   `json:"status"`
	StatusMessage       string   `json:"statusMessage"`
	IsReplicaSet        bool     `json:"isReplicaSet"`
}

func mongoApply(d *schema.ResourceData, m *mongoCluster) {
	_ = d.Set("cluster_name", m.ClusterName)
	_ = d.Set("region", m.Region)
	_ = d.Set("regions", m.Regions)
	_ = d.Set("consistency", m.Consistency)
	_ = d.Set("max_staleness_seconds", m.MaxStalenessSeconds)
	_ = d.Set("engine_version", m.EngineVersion)
	_ = d.Set("sku", m.Sku)
	_ = d.Set("storage_gb", m.StorageGb)
	_ = d.Set("database_name", m.DatabaseName)
	_ = d.Set("status", m.Status)
	_ = d.Set("status_message", m.StatusMessage)
	_ = d.Set("is_replica_set", m.IsReplicaSet)
	_ = d.Set("replica_set_name", m.ReplicaSetName)
	_ = d.Set("host", m.Host)
	_ = d.Set("port", m.Port)
	_ = d.Set("read_concern", m.ReadConcern)
	_ = d.Set("write_concern", m.WriteConcern)
	_ = d.Set("read_preference", m.ReadPreference)
}

// mongoReadConnection fetches the connection string separately: it carries the
// administrator password, so the API only hands it out on an explicit request.
func mongoReadConnection(c *client.Client, d *schema.ResourceData) {
	var conn struct {
		ConnectionString string `json:"connectionString"`
	}
	if err := c.Do(context.Background(), "GET", "/api/Mongo/"+url.PathEscape(d.Id())+"/connection", nil, &conn); err == nil {
		_ = d.Set("connection_string", conn.ConnectionString)
	}
}

func mongoCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	regions := stringList(d, "regions")
	region := d.Get("region").(string)
	if region == "" {
		if len(regions) > 0 {
			region = regions[0]
		} else if len(c.Regions) > 0 {
			region = c.Regions[0]
		}
	}

	payload := map[string]any{
		"clusterName":         d.Get("cluster_name").(string),
		"region":              region,
		"consistency":         d.Get("consistency").(string),
		"maxStalenessSeconds": d.Get("max_staleness_seconds").(int),
		"engineVersion":       d.Get("engine_version").(string),
		"sku":                 d.Get("sku").(string),
		"storageGb":           d.Get("storage_gb").(int),
	}
	if len(regions) > 0 {
		payload["regions"] = regions
	}
	if v, ok := d.GetOk("database_name"); ok && v.(string) != "" {
		payload["databaseName"] = v.(string)
	}
	if v, ok := d.GetOk("vnet_name"); ok && v.(string) != "" {
		payload["vNetName"] = v.(string)
	}

	var m mongoCluster
	if err := mergeExtras(d, payload, "hiok_mongo_cluster"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/Mongo", payload, &m); err != nil {
		return diag.FromErr(err)
	}
	if m.ID == "" {
		return diag.Errorf("the API accepted the request but returned no cluster")
	}

	d.SetId(m.ID)
	mongoApply(d, &m)
	mongoReadConnection(c, d)

	// Degraded means some members are up and the cluster is usable; surfacing it as a
	// warning keeps the apply honest without failing a cluster that works.
	if m.Status == "Degraded" && m.StatusMessage != "" {
		return diag.Diagnostics{{
			Severity: diag.Warning,
			Summary:  "MongoDB cluster came up degraded",
			Detail:   m.StatusMessage,
		}}
	}
	return nil
}

func mongoRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var m mongoCluster
	if err := c.Do(context.Background(), "GET", "/api/Mongo/"+url.PathEscape(d.Id()), nil, &m); err != nil {
		if !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	mongoApply(d, &m)
	mongoReadConnection(c, d)
	return nil
}

func mongoUpdate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	// Consistency is the only thing that can change in place; everything else is ForceNew.
	if d.HasChange("consistency") || d.HasChange("max_staleness_seconds") {
		payload := map[string]any{
			"consistency":         d.Get("consistency").(string),
			"maxStalenessSeconds": d.Get("max_staleness_seconds").(int),
		}
		var m mongoCluster
		if err := c.Do(context.Background(), "PUT", "/api/Mongo/"+url.PathEscape(d.Id())+"/consistency", payload, &m); err != nil {
			return diag.FromErr(err)
		}
		mongoApply(d, &m)
		// The connection string encodes the concerns, so it changed with them.
		mongoReadConnection(c, d)
	}
	return nil
}

func mongoDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/Mongo/"+url.PathEscape(d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── YugabyteDB ──────────────────────────────────────────────────────────────

func resourceYugabyteCluster() *schema.Resource {
	return &schema.Resource{
		Description: "A managed YugabyteDB cluster — distributed SQL over the PostgreSQL wire " +
			"protocol. One node per region; replication factor follows the count and is kept odd.",
		CreateContext: yugabyteCreate,
		ReadContext:   yugabyteRead,
		DeleteContext: yugabyteDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"cluster_name": {Type: schema.TypeString, Required: true, ForceNew: true},
			"regions": {
				Type: schema.TypeList, Optional: true, ForceNew: true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "One node per region. Three regions give RF 3; two give RF 1, because a majority needs an odd number.",
			},
			"region":         {Type: schema.TypeString, Optional: true, ForceNew: true},
			"sku":            {Type: schema.TypeString, Optional: true, Default: "small", ForceNew: true},
			"engine_version": {Type: schema.TypeString, Optional: true, ForceNew: true},
			"database_name":  {Type: schema.TypeString, Optional: true, ForceNew: true},
			"storage_gb":     {Type: schema.TypeInt, Optional: true, Default: 20, ForceNew: true},
			"vnet_name":      {Type: schema.TypeString, Optional: true, ForceNew: true},

			"status":             {Type: schema.TypeString, Computed: true},
			"status_message":     {Type: schema.TypeString, Computed: true},
			"replication_factor": {Type: schema.TypeInt, Computed: true},
			"host":               {Type: schema.TypeString, Computed: true},
			"ysql_port":          {Type: schema.TypeInt, Computed: true},
			"ycql_port":          {Type: schema.TypeInt, Computed: true},
			"admin_ui_url":       {Type: schema.TypeString, Computed: true},
		},
	}
}

type yugabyteCluster struct {
	ID                string   `json:"id"`
	ClusterName       string   `json:"clusterName"`
	Region            string   `json:"region"`
	Regions           []string `json:"regions"`
	ReplicationFactor int      `json:"replicationFactor"`
	EngineVersion     string   `json:"engineVersion"`
	DatabaseName      string   `json:"databaseName"`
	Host              string   `json:"host"`
	YsqlPort          int      `json:"ysqlPort"`
	YcqlPort          int      `json:"ycqlPort"`
	Sku               string   `json:"sku"`
	StorageGb         int      `json:"storageGb"`
	Status            string   `json:"status"`
	StatusMessage     string   `json:"statusMessage"`
	AdminUIURL        string   `json:"adminUiUrl"`
}

func yugabyteApply(d *schema.ResourceData, y *yugabyteCluster) {
	_ = d.Set("cluster_name", y.ClusterName)
	_ = d.Set("region", y.Region)
	_ = d.Set("regions", y.Regions)
	_ = d.Set("replication_factor", y.ReplicationFactor)
	_ = d.Set("engine_version", y.EngineVersion)
	_ = d.Set("database_name", y.DatabaseName)
	_ = d.Set("host", y.Host)
	_ = d.Set("ysql_port", y.YsqlPort)
	_ = d.Set("ycql_port", y.YcqlPort)
	_ = d.Set("sku", y.Sku)
	_ = d.Set("storage_gb", y.StorageGb)
	_ = d.Set("status", y.Status)
	_ = d.Set("status_message", y.StatusMessage)
	_ = d.Set("admin_ui_url", y.AdminUIURL)
}

func yugabyteCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	regions := stringList(d, "regions")
	region := d.Get("region").(string)
	if region == "" {
		if len(regions) > 0 {
			region = regions[0]
		} else if len(c.Regions) > 0 {
			region = c.Regions[0]
		}
	}

	payload := map[string]any{
		"clusterName": d.Get("cluster_name").(string),
		"region":      region,
		"sku":         d.Get("sku").(string),
		"storageGb":   d.Get("storage_gb").(int),
	}
	if len(regions) > 0 {
		payload["regions"] = regions
	}
	for key, field := range map[string]string{
		"engineVersion": "engine_version",
		"databaseName":  "database_name",
		"vNetName":      "vnet_name",
	} {
		if v, ok := d.GetOk(field); ok && v.(string) != "" {
			payload[key] = v.(string)
		}
	}

	var resp struct {
		Data yugabyteCluster `json:"data"`
	}
	if err := mergeExtras(d, payload, "hiok_yugabyte_cluster"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/Yugabyte", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	if resp.Data.ID == "" {
		return diag.Errorf("the API accepted the request but returned no cluster")
	}

	d.SetId(resp.Data.ID)
	yugabyteApply(d, &resp.Data)

	if resp.Data.Status == "Degraded" && resp.Data.StatusMessage != "" {
		return diag.Diagnostics{{
			Severity: diag.Warning,
			Summary:  "YugabyteDB cluster came up degraded",
			Detail:   resp.Data.StatusMessage,
		}}
	}
	return nil
}

func yugabyteRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data yugabyteCluster `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", "/api/Yugabyte/"+url.PathEscape(d.Id()), nil, &resp); err != nil || resp.Data.ID == "" {
		if err != nil && !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	yugabyteApply(d, &resp.Data)
	return nil
}

func yugabyteDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/Yugabyte/"+url.PathEscape(d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
