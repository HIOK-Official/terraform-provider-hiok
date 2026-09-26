package provider

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/customdiff"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// Options the API accepts on create that the hand-written resources did not expose.
// Declared once here, added to each resource's schema, and merged into its create
// payload, so the resources keep their own logic and gain the rest of the API.
var createExtras = map[string][]restField{
	"hiok_analytics_cluster": append([]restField{
		str("admin_password", "adminPassword", "Admin password (default: generated).").sensitive(),
	}, placement()...),
	"hiok_bastion": {
		str("vnet_id", "vnetId", "Virtual network to attach to, by id."),
		num("vlan_tag", "vlanTag", "VLAN tag on the network's bridge."),
	},
	"hiok_container": {
		strList("entrypoint", "entrypoint", "Overrides the image's entrypoint."),
		strMap("labels", "labels", "Container labels."),
		jsonField("ports", "ports", `Published ports: jsonencode([{hostPort = 8080, containerPort = 80, protocol = "tcp"}]).`),
		jsonField("volumes", "volumes", `Mounts: jsonencode([{type = "storage", source = "<account>/<folder>", destination = "/data", readOnly = false}]) — type volume, storage or tmpfs.`),
		str("restart_policy", "restartPolicy", "no, on-failure, always or unless-stopped (default)."),
		strList("networks", "networks", "Networks to join."),
		str("network_mode", "networkMode", "bridge (default) or none."),
		num("memory_bytes", "memoryBytes", "Memory limit in bytes."),
		flt("cpus", "cpus", "CPU limit, e.g. 0.5."),
		str("user", "user", `User the process runs as, e.g. "101:101".`),
		str("working_dir", "workingDir", "Working directory of the process."),
		str("stop_signal", "stopSignal", "Signal sent to stop the container (default SIGTERM)."),
		str("domainname", "domainname", "Domain name inside the container."),
		boolean("tty", "tty", false, "Allocate a terminal."),
	},
	"hiok_container_app": append([]restField{
		boolean("cloudflare_enabled", "cloudflareEnabled", false, "Serve through the CDN."),
		jsonField("volumes", "volumes", "Volume mounts (JSON)."),
		jsonField("health_check", "healthCheck", `jsonencode({path = "/healthz", intervalSeconds = 10})`),
		jsonField("sidecars", "sidecars", "Sidecar containers (JSON)."),
		str("custom_domain", "customDomain", "A domain of yours to serve the app on."),
		boolean("otel_enabled", "otelEnabled", false, "Inject OpenTelemetry settings pointing at the region's collector."),
		boolean("dapr_enabled", "daprEnabled", false, "Run a Dapr sidecar."),
		str("dapr_app_id", "daprAppId", ""),
	}, placement()...),
	"hiok_container_app_environment": placement(),
	"hiok_mongo_cluster":             placement(),
	"hiok_yugabyte_cluster": append([]restField{
		str("admin_username", "adminUsername", ""),
		str("admin_password", "adminPassword", "Default: generated.").sensitive(),
		num("v_cpu", "vCpu", "vCPUs per node."),
		flt("ram_gb", "ramGb", "RAM per node."),
		str("vnet_address", "vNetAddress", "Address space when creating a network."),
	}, placement()...),
	"hiok_servicebus_namespace": {
		strList("replica_regions", "replicaRegions", "Regions holding a replica."),
		jsonField("broker_config", "brokerConfig", "Bring-your-own broker settings (JSON)."),
	},
	"hiok_virtual_network": {
		str("forward_mode", "forwardMode", "nat, route or isolated."),
		str("bridge_name", "bridgeName", "Bridge name on the host (default: derived from the name)."),
	},
	"hiok_kubernetes_cluster": placement(),
	"hiok_container_registry": append([]restField{
		str("admin_username", "adminUsername", "Default: hiokadmin."),
		str("admin_password", "adminPassword", "Default: generated (read it with data.hiok_container_registry_credentials).").sensitive(),
	}, placement()...),
	"hiok_cache": append([]restField{
		str("engine_version", "engineVersion", ""),
		strList("regions", "regions", "Regions holding a replica."),
		num("max_clients", "maxClients", ""),
		boolean("persistence_enabled", "persistenceEnabled", false, "Write snapshots to disk."),
		num("snapshot_seconds", "snapshotSeconds", "Seconds between snapshots."),
	}, placement()...),
	"hiok_stream_job": append([]restField{
		str("engine_version", "engineVersion", ""),
		str("serving_version", "servingVersion", ""),
		num("checkpoint_interval", "checkpointInterval", "Seconds between checkpoints."),
		str("network_mode", "networkMode", "public or vnet."),
		str("vnet_name", "vNetName", ""),
		str("subnet_name", "subnetName", ""),
		str("admin_username", "adminUsername", ""),
		str("admin_password", "adminPassword", "").sensitive(),
		str("query_sql", "querySql", "The job's SQL."),
		str("artifact_uri", "artifactUri", "JAR/zip to run instead of SQL."),
		str("entry_class", "entryClass", "Main class of the artifact."),
		jsonField("inputs", "inputs", "Input streams (JSON)."),
		jsonField("outputs", "outputs", "Output sinks (JSON)."),
	}, placement()...),
	"hiok_postgres_database": placement(),
	"hiok_virtual_machine": {
		num("disk_size_gb", "diskSizeGb", "OS disk size."),
		str("password", "password", "Login password, when auth_type is password.").sensitive(),
		str("auth_type", "authType", "ssh (default) or password."),
		str("mac_address", "macAddress", ""),
		str("subnet_ip", "subnetIp", "Static address in the subnet."),
		str("subnet_mask", "subnetMask", ""),
		str("gateway", "gateway", ""),
		boolean("use_ovs_bridge", "useOVSBridge", false, ""),
		str("ovs_bridge_name", "ovsBridgeName", ""),
	},
	"hiok_vpn_gateway": {
		str("subnet_id", "subnetId", ""),
		str("subnet_cidr", "subnetCIDR", ""),
	},
	"hiok_vpn_client": {
		num("certificate_validity_days", "certificateValidityDays", "Client certificate lifetime."),
	},
}

// addExtras puts each resource's extra options into its schema. They are applied at
// creation, so a change replaces the resource unless the resource updates in place.
func addExtras(p *schema.Provider) {
	for name, fields := range createExtras {
		r := p.ResourcesMap[name]
		if r == nil {
			continue
		}
		spec := &restSpec{Fields: fields}
		extra := spec.resource().Schema
		// Resources that track imports replace on a create-time change only once the
		// value is recorded, so an import is not followed by a replacement.
		var createOnly []string
		for attr, sc := range extra {
			if _, exists := r.Schema[attr]; exists {
				continue
			}
			if _, hasFlag := r.Schema["imported"]; hasFlag {
				// No default either: an imported resource has none recorded, and the
				// API applies its own when the key is left out of the payload.
				sc.ForceNew, sc.Default = false, nil
				createOnly = append(createOnly, attr)
			} else {
				sc.ForceNew = true // sent on create only
			}
			// These resources do not read extras back, so a list or map extra must stay
			// plain optional: computed, it would plan as unknown and force a new
			// resource on every run.
			if sc.Type == schema.TypeList || sc.Type == schema.TypeMap {
				sc.Computed = false
			}
			r.Schema[attr] = sc
		}
		if len(createOnly) > 0 {
			if r.CustomizeDiff != nil {
				r.CustomizeDiff = customdiff.All(r.CustomizeDiff, createTimeOnly(createOnly...))
			} else {
				r.CustomizeDiff = createTimeOnly(createOnly...)
			}
		}
	}
}

// mergeExtras adds the configured extra options to a create payload.
func mergeExtras(d *schema.ResourceData, payload map[string]any, resource string) error {
	spec := &restSpec{Fields: createExtras[resource]}
	extra, err := spec.payload(d, false)
	if err != nil {
		return err
	}
	for _, f := range createExtras[resource] {
		v, ok := extra[f.JSON]
		if !ok {
			continue
		}
		// A configured value wins over the resource's own default for that key.
		if _, set := payload[f.JSON]; !set {
			payload[f.JSON] = v
		} else if _, configured := d.GetOk(f.Attr); configured {
			payload[f.JSON] = v
		}
	}
	return nil
}
