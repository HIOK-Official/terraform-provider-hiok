package provider

import (
	"context"
	"net/url"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// ── PostgreSQL ──────────────────────────────────────────────────────────────

func resourcePostgresDatabase() *schema.Resource {
	return &schema.Resource{
		Description:   "A managed PostgreSQL server.",
		CreateContext: postgresCreate,
		ReadContext:   postgresRead,
		DeleteContext: postgresDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"server_name": {Type: schema.TypeString, Required: true, ForceNew: true},
			"region":      {Type: schema.TypeString, Optional: true, ForceNew: true},
			"admin_username": {
				Type: schema.TypeString, Optional: true, Default: "pgadmin", ForceNew: true,
			},
			"admin_password": {
				Type: schema.TypeString, Optional: true, ForceNew: true, Sensitive: true,
				Description: "Generated when omitted, and readable afterwards from connection_string.",
			},
			"database_name":    {Type: schema.TypeString, Optional: true, ForceNew: true},
			"postgres_version": {Type: schema.TypeString, Optional: true, Default: "16", ForceNew: true},
			"sku":              {Type: schema.TypeString, Optional: true, Default: "Burstable_B1ms", ForceNew: true},
			"storage_gb":       {Type: schema.TypeInt, Optional: true, Default: 32, ForceNew: true},

			"status": {Type: schema.TypeString, Computed: true},
			"host":   {Type: schema.TypeString, Computed: true},
			"port":   {Type: schema.TypeInt, Computed: true},
			"connection_string": {
				Type: schema.TypeString, Computed: true, Sensitive: true,
				Description: "Carries the administrator password, so it is marked sensitive.",
			},
		},
	}
}

type postgresDatabase struct {
	ID               string `json:"id"`
	ServerName       string `json:"serverName"`
	Region           string `json:"region"`
	Status           string `json:"status"`
	PostgresVersion  string `json:"postgresVersion"`
	AdminUsername    string `json:"adminUsername"`
	DatabaseName     string `json:"databaseName"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Sku              string `json:"sku"`
	StorageGb        int    `json:"storageGb"`
	ConnectionString string `json:"connectionString"`
}

func postgresApply(d *schema.ResourceData, p *postgresDatabase) {
	_ = d.Set("server_name", p.ServerName)
	_ = d.Set("region", p.Region)
	_ = d.Set("status", p.Status)
	_ = d.Set("postgres_version", p.PostgresVersion)
	_ = d.Set("admin_username", p.AdminUsername)
	_ = d.Set("database_name", p.DatabaseName)
	_ = d.Set("host", p.Host)
	_ = d.Set("port", p.Port)
	_ = d.Set("sku", p.Sku)
	_ = d.Set("storage_gb", p.StorageGb)
	_ = d.Set("connection_string", p.ConnectionString)
}

func postgresCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	region := d.Get("region").(string)
	if region == "" && len(c.Regions) > 0 {
		region = c.Regions[0]
	}

	payload := map[string]any{
		"serverName":      d.Get("server_name").(string),
		"region":          region,
		"adminUsername":   d.Get("admin_username").(string),
		"postgresVersion": d.Get("postgres_version").(string),
		"sku":             d.Get("sku").(string),
		"storageGb":       d.Get("storage_gb").(int),
	}
	for key, field := range map[string]string{
		"adminPassword": "admin_password",
		"databaseName":  "database_name",
	} {
		if v, ok := d.GetOk(field); ok && v.(string) != "" {
			payload[key] = v.(string)
		}
	}

	var resp struct {
		Data postgresDatabase `json:"data"`
	}
	if err := mergeExtras(d, payload, "hiok_postgres_database"); err != nil {
		return diag.FromErr(err)
	}
	if err := c.Do(context.Background(), "POST", "/api/PostgresDatabase", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	if resp.Data.ID == "" {
		return diag.Errorf("the API accepted the request but returned no database")
	}

	d.SetId(resp.Data.ID)
	postgresApply(d, &resp.Data)
	return nil
}

func postgresRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	var resp struct {
		Data postgresDatabase `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", "/api/PostgresDatabase/"+url.PathEscape(d.Id()), nil, &resp); err != nil || resp.Data.ID == "" {
		if err != nil && !client.IsNotFound(err) {
			return diag.FromErr(err)
		}
		d.SetId("")
		return nil
	}
	postgresApply(d, &resp.Data)
	return nil
}

func postgresDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/PostgresDatabase/"+url.PathEscape(d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// ── IoT ─────────────────────────────────────────────────────────────────────

// resourceIoTDevice registers a device identity in a hub.
//
// The connection string is returned once at creation and is the only time the shared
// access key is handed over in full, so it is recorded in state — which is why state
// for this resource must be treated as a secret.
func resourceIoTDevice() *schema.Resource {
	return &schema.Resource{
		Description:   "A device identity in an IoT hub, with the connection string it authenticates by.",
		CreateContext: iotDeviceCreate,
		ReadContext:   iotDeviceRead,
		DeleteContext: iotDeviceDelete,

		Schema: map[string]*schema.Schema{
			"hub_id":    {Type: schema.TypeString, Required: true, ForceNew: true},
			"device_id": {Type: schema.TypeString, Required: true, ForceNew: true},

			"status": {Type: schema.TypeString, Computed: true},
			"connection_string": {
				Type: schema.TypeString, Computed: true, Sensitive: true,
				Description: "Carries the device's shared access key.",
			},
		},
	}
}

type iotDevice struct {
	DeviceID         string `json:"deviceId"`
	Status           string `json:"status"`
	ConnectionString string `json:"connectionString"`
}

func iotDeviceCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	hubID := d.Get("hub_id").(string)
	deviceID := d.Get("device_id").(string)

	var device iotDevice
	if err := c.Do(context.Background(), "POST", "/api/iothub/"+url.PathEscape(hubID)+"/devices",
		map[string]any{"deviceId": deviceID}, &device); err != nil {
		return diag.FromErr(err)
	}

	// A device is addressed by hub and device id, not by a server-side identifier.
	d.SetId(hubID + "/" + deviceID)
	_ = d.Set("status", device.Status)
	_ = d.Set("connection_string", device.ConnectionString)
	return nil
}

func iotDeviceRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	hubID := d.Get("hub_id").(string)
	deviceID := d.Get("device_id").(string)

	var conn struct {
		ConnectionString string `json:"connectionString"`
	}
	if err := c.Do(context.Background(), "GET", "/api/iothub/"+url.PathEscape(hubID)+"/devices/"+url.PathEscape(deviceID)+"/connection-string",
		nil, &conn); err != nil {
		d.SetId("")
		return nil
	}
	_ = d.Set("connection_string", conn.ConnectionString)
	return nil
}

func iotDeviceDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	hubID := d.Get("hub_id").(string)
	deviceID := d.Get("device_id").(string)
	if err := c.Do(context.Background(), "DELETE", "/api/iothub/"+url.PathEscape(hubID)+"/devices/"+url.PathEscape(deviceID), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

// resourceDpsEnrollment lets devices provision themselves into a hub by presenting an
// attestation key, rather than each identity being created ahead of time.
func resourceDpsEnrollment() *schema.Resource {
	return &schema.Resource{
		Description: "A Device Provisioning Service enrollment. Devices present its attestation " +
			"key and are provisioned into the target hub automatically.",
		CreateContext: dpsEnrollmentCreate,
		ReadContext:   dpsEnrollmentRead,
		DeleteContext: dpsEnrollmentDelete,
		Importer:      &schema.ResourceImporter{StateContext: schema.ImportStatePassthroughContext},

		Schema: map[string]*schema.Schema{
			"name": {Type: schema.TypeString, Required: true, ForceNew: true},
			"enrollment_type": {
				Type: schema.TypeString, Optional: true, Default: "individual", ForceNew: true,
				Description: "\"individual\" matches one registration id; \"group\" matches any device presenting the key.",
			},
			"registration_id": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Required for an individual enrollment — the id the device presents.",
			},
			"target_hub_id": {Type: schema.TypeString, Required: true, ForceNew: true},
			"device_id_prefix": {
				Type: schema.TypeString, Optional: true, ForceNew: true,
				Description: "Prefixed to the registration id when naming the provisioned device.",
			},

			"primary_key": {
				Type: schema.TypeString, Computed: true, Sensitive: true,
				Description: "The attestation key devices present when registering.",
			},
			"provisioning_status": {Type: schema.TypeString, Computed: true},
		},
	}
}

type dpsEnrollment struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	EnrollmentType     string `json:"enrollmentType"`
	RegistrationID     string `json:"registrationId"`
	TargetHubID        string `json:"targetHubId"`
	DeviceIDPrefix     string `json:"deviceIdPrefix"`
	PrimaryKey         string `json:"primaryKey"`
	ProvisioningStatus string `json:"provisioningStatus"`
}

func dpsEnrollmentApply(d *schema.ResourceData, e *dpsEnrollment) {
	_ = d.Set("name", e.Name)
	_ = d.Set("enrollment_type", e.EnrollmentType)
	_ = d.Set("target_hub_id", e.TargetHubID)
	_ = d.Set("provisioning_status", e.ProvisioningStatus)
	if e.RegistrationID != "" {
		_ = d.Set("registration_id", e.RegistrationID)
	}
	if e.DeviceIDPrefix != "" {
		_ = d.Set("device_id_prefix", e.DeviceIDPrefix)
	}
	if e.PrimaryKey != "" {
		_ = d.Set("primary_key", e.PrimaryKey)
	}
}

func dpsEnrollmentCreate(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	payload := map[string]any{
		"name":           d.Get("name").(string),
		"enrollmentType": d.Get("enrollment_type").(string),
		"targetHubId":    d.Get("target_hub_id").(string),
	}
	for key, field := range map[string]string{
		"registrationId": "registration_id",
		"deviceIdPrefix": "device_id_prefix",
	} {
		if v, ok := d.GetOk(field); ok && v.(string) != "" {
			payload[key] = v.(string)
		}
	}

	var resp struct {
		Data dpsEnrollment `json:"data"`
	}
	if err := c.Do(context.Background(), "POST", "/api/Dps/enrollments", payload, &resp); err != nil {
		return diag.FromErr(err)
	}
	if resp.Data.ID == "" {
		return diag.Errorf("the API accepted the request but returned no enrollment")
	}

	d.SetId(resp.Data.ID)
	dpsEnrollmentApply(d, &resp.Data)
	return nil
}

func dpsEnrollmentRead(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)

	// Enrollments are listed rather than served by id, so the read filters.
	var resp struct {
		Data []dpsEnrollment `json:"data"`
	}
	if err := c.Do(context.Background(), "GET", "/api/Dps/enrollments", nil, &resp); err != nil {
		return diag.FromErr(err)
	}
	for _, enrollment := range resp.Data {
		if enrollment.ID == d.Id() {
			dpsEnrollmentApply(d, &enrollment)
			return nil
		}
	}
	d.SetId("")
	return nil
}

func dpsEnrollmentDelete(ctx context.Context, d *schema.ResourceData, meta any) diag.Diagnostics {
	c := meta.(*client.Client)
	if err := c.Do(context.Background(), "DELETE", "/api/Dps/enrollments/"+url.PathEscape(d.Id()), nil, nil); err != nil && !client.IsNotFound(err) {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}
