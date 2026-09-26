package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

	"github.com/HIOK-Official/terraform-provider-hiok/internal/client"
)

// New returns the provider schema and wiring.
func New() *schema.Provider {
	p := &schema.Provider{
		Schema: map[string]*schema.Schema{
			"endpoint": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_ENDPOINT", nil),
				Description: "Base URL of the HIOK API, e.g. https://hiokcloud.com. Can be set with HIOK_ENDPOINT.",
			},
			"token": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_TOKEN", nil),
				Description: "API token. Takes precedence over email/password. Can be set with HIOK_TOKEN.",
			},
			"email": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_EMAIL", nil),
				Description: "Account email, used to obtain a token when none is set. Can be set with HIOK_EMAIL.",
			},
			"password": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_PASSWORD", nil),
				Description: "Account password, used to obtain a token when none is set. Can be set with HIOK_PASSWORD.",
			},
			"client_id": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_CLIENT_ID", nil),
				Description: "Service principal client ID (console: Identity → Service principals) — the credential for CI/CD. Can be set with HIOK_CLIENT_ID.",
			},
			"client_secret": {
				Type:        schema.TypeString,
				Optional:    true,
				Sensitive:   true,
				DefaultFunc: schema.EnvDefaultFunc("HIOK_CLIENT_SECRET", nil),
				Description: "Service principal client secret. Can be set with HIOK_CLIENT_SECRET.",
			},
			"regions": {
				Type:        schema.TypeList,
				Optional:    true,
				Elem:        &schema.Schema{Type: schema.TypeString},
				Description: "Default region for resources that do not set their own (the first entry is used). Defaults to the first available region reported by the API, e.g. \"canada\".",
			},
		},

		ResourcesMap: map[string]*schema.Resource{
			"hiok_virtual_machine": resourceVirtualMachine(),
			"hiok_container":       resourceContainer(),
			"hiok_virtual_network": resourceVirtualNetwork(),
			"hiok_storage_account": resourceStorageAccount(),

			// Messaging
			"hiok_servicebus_namespace":  resourceServiceBusNamespace(),
			"hiok_servicebus_queue":      resourceServiceBusQueue(),
			"hiok_event_stream":          resourceEventStream(),
			"hiok_event_subscription":    resourceEventSubscription(),
			"hiok_communication_service": resourceCommunicationService(),
			"hiok_communication_domain":  resourceCommunicationDomain(),
			"hiok_bastion":               resourceBastion(),
			"hiok_analytics_cluster":     resourceAnalyticsCluster(),
			"hiok_streaming_endpoint":    resourceStreamingEndpoint(),

			// Networking
			"hiok_public_ip":   resourcePublicIP(),
			"hiok_vpn_gateway": resourceVPNGateway(),
			"hiok_vpn_client":  resourceVPNClient(),

			// Databases
			"hiok_mongo_cluster":    resourceMongoCluster(),
			"hiok_yugabyte_cluster": resourceYugabyteCluster(),

			// Security
			"hiok_key_vault": resourceKeyVault(),

			// Container Apps
			"hiok_container_app_environment": resourceContainerAppEnvironment(),
			"hiok_container_app":             resourceContainerApp(),

			// Databases
			"hiok_postgres_database": resourcePostgresDatabase(),

			// IoT
			"hiok_iot_device":     resourceIoTDevice(),
			"hiok_dps_enrollment": resourceDpsEnrollment(),

			// The rest of the platform. These were creatable in the console and
			// nowhere else, so anything managed as code stopped at about half the
			// catalogue and the remainder had to be clicked into existence.
			"hiok_kubernetes_cluster": resourceKubernetesCluster(),
			"hiok_container_registry": resourceContainerRegistry(),
			"hiok_cache":              resourceCache(),
			"hiok_stream_job":         resourceStreamJob(),
			"hiok_mysql_database":     relationalResource("MySQL", "/api/MySqlDatabase", "Burstable_B1ms"),
			"hiok_sqlserver_database": relationalResource("SQL Server", "/api/SqlServerDatabase", "Burstable_B1ms"),

			// Newer capabilities, described as code from the start rather than
			// added to the provider a year after the feature.
			"hiok_stream_destination": resourceStreamDestination(),
			"hiok_docker_stack":       resourceDockerStack(),
			"hiok_swarm_service":      resourceSwarmService(),
		},

		DataSourcesMap: map[string]*schema.Resource{
			"hiok_regions":         dataSourceRegions(),
			"hiok_vm_images":       dataSourceVmImages(),
			"hiok_virtual_machine": dataSourceVirtualMachine(),
			"hiok_virtual_network": dataSourceVirtualNetwork(),
			"hiok_storage_account": dataSourceStorageAccount(),
			"hiok_container":       dataSourceContainer(),
			"hiok_ip_pools":        dataSourceIPPools(),
		},

		ConfigureContextFunc: configure,
	}
	// Resources declared on the REST engine (resource_catalog.go).
	for name, r := range catalogResources() {
		p.ResourcesMap[name] = r
	}
	for name, r := range catalogDataSources() {
		p.DataSourcesMap[name] = r
	}
	addExtras(p)
	// Lists these hand-written resources read back from the API: the API fills them
	// in when the configuration leaves them out, so they accept its value.
	for resource, fields := range map[string][]string{
		"hiok_key_vault":        {"regions", "allowed_cidrs"},
		"hiok_mongo_cluster":    {"regions"},
		"hiok_yugabyte_cluster": {"regions"},
	} {
		for _, f := range fields {
			if sc := p.ResourcesMap[resource].Schema[f]; sc != nil && sc.Optional {
				sc.Computed = true
			}
		}
	}
	for _, r := range p.ResourcesMap {
		acceptServerDefaults(r.Schema)
	}
	return p
}

func configure(_ context.Context, d *schema.ResourceData) (any, diag.Diagnostics) {
	regions := make([]string, 0)
	for _, r := range d.Get("regions").([]any) {
		if s, ok := r.(string); ok && s != "" {
			regions = append(regions, s)
		}
	}

	c, err := client.New(
		d.Get("endpoint").(string),
		d.Get("token").(string),
		d.Get("email").(string),
		d.Get("password").(string),
		regions,
		client.WithServicePrincipal(d.Get("client_id").(string), d.Get("client_secret").(string)),
	)
	if err != nil {
		return nil, diag.FromErr(err)
	}
	return c, nil
}

// acceptServerDefaults makes every optional field without a default also computed.
//
// The API fills in what a configuration leaves out — a region, an item type, a
// product name. With the field merely optional, the next plan read "canada -> null"
// and, on a field that forces a new resource, replaced it: every re-apply of an
// unchanged configuration destroyed and recreated key vaults, namespaces and jobs.
// Optional + computed accepts the API's value when the configuration says nothing.
func acceptServerDefaults(s map[string]*schema.Schema) {
	for _, field := range s {
		// Primitives only. A list or map left out of the configuration is empty in
		// state already; made computed, one that the resource never reads back plans
		// as "known after apply" and — on a field that forces a new resource —
		// replaces it on every run, which is exactly what this is here to prevent.
		primitive := field.Type == schema.TypeString || field.Type == schema.TypeInt ||
			field.Type == schema.TypeBool || field.Type == schema.TypeFloat
		if primitive && field.Optional && !field.Computed && field.Default == nil && field.DefaultFunc == nil {
			field.Computed = true
		}
		if res, ok := field.Elem.(*schema.Resource); ok {
			acceptServerDefaults(res.Schema)
		}
	}
}
