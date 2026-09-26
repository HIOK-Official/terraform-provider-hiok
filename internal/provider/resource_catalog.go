package provider

import "github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"

// Resources defined declaratively on the REST engine (rest_resource.go). Field names
// follow the API's request models; each description says what the value does.

func placement() []restField {
	return []restField{
		str("subscription_id", "subscriptionId", "Subscription the resource is billed to.").forceNew(),
		str("resource_group_id", "resourceGroupId", "Resource group the resource is placed in.").forceNew(),
	}
}

func catalogResources() map[string]*schema.Resource {
	specs := map[string]*restSpec{
		// ── compute ─────────────────────────────────────────────────────────
		"hiok_container_job": {
			Description: "A container run to completion on demand or on a cron schedule, with secrets from Key Vault, " +
				"mounted storage, retries, a timeout and an email when it fails.",
			CreatePath: "/api/container-jobs", ReadPath: "/api/container-jobs/{id}",
			UpdateMethod: "PUT", UpdatePath: "/api/container-jobs/{id}", DeletePath: "/api/container-jobs/{id}",
			Fields: append([]restField{
				reqStr("name", "name", "2-41 lowercase letters, digits or hyphens.").forceNew(),
				str("description", "description", ""),
				str("region", "region", "Region the job runs in (default canada).").forceNew(),
				reqStr("image", "image", "Image reference, e.g. python:3.12-slim or <registry>:5000/batch:1.0 (private HIOK registries are pulled with their credentials)."),
				strList("command", "command", "Command and arguments; empty runs the image's own."),
				strMap("environment", "environment", "Plain environment variables. Put secrets in `secrets` instead."),
				jsonField("secrets", "secrets", `Key Vault secrets as environment variables: jsonencode([{env = "B2_APPKEY", vaultId = hiok_key_vault.x.id, secret = "b2-appkey"}]).`),
				jsonField("volumes", "volumes", `Mounts: jsonencode([{type = "storage", source = "<account>/<folder>", destination = "/data", readOnly = false}]).`),
				flt("cpus", "cpus", "CPU limit, e.g. 0.5."),
				num("memory_mb", "memoryMb", "Memory limit in MB."),
				num("timeout_minutes", "timeoutMinutes", "A run still going after this is stopped."),
				num("max_retries", "maxRetries", "Retries after a failed run."),
				str("schedule", "schedule", "Cron expression (5 fields), e.g. */15 * * * *. Empty = on demand only."),
				str("time_zone", "timeZone", "IANA time zone for the schedule, e.g. Asia/Kolkata."),
				boolean("enabled", "enabled", true, "Whether the schedule fires."),
				boolean("notify_on_failure", "notifyOnFailure", true, "Email when a run fails."),
				str("notify_email", "notifyEmail", "Where failure emails go (default: the owner)."),
				computed("next_run_at", "nextRunAt", fString, "Next scheduled run (UTC)."),
				computed("status", "status", fString, "Last run's outcome."),
			}, placement()...),
		},
		"hiok_vm_snapshot": {
			Description: "A restore point of a virtual machine. Destroying it deletes the snapshot.",
			CreatePath:  "/api/VirtualMachine/{vm_name}/snapshots", ListPath: "/api/VirtualMachine/{vm_name}/snapshots",
			ListMatch: "name", ListMatchAttr: "name", IDFormat: "{vm_name}/{name}",
			DeletePath: "/api/VirtualMachine/{vm_name}/snapshots/{name}",
			Fields: []restField{
				parent("vm_name", "The VM's name."),
				reqStr("name", "name", "Snapshot name."),
				str("description", "description", ""),
				boolean("include_memory", "includeMemory", false, "Capture RAM too, so a restore resumes execution."),
				computed("state", "state", fString, "What the VM was doing when it was taken."),
				computed("created_at", "createdAt", fString, ""),
			},
		},
		"hiok_vm_network_rule": {
			Description: "A firewall or routing rule on a virtual machine.",
			CreatePath:  "/api/VirtualMachine/{vm_name}/network-rules", ListPath: "/api/VirtualMachine/{vm_name}/network-rules",
			UpdateMethod: "PUT", UpdatePath: "/api/VirtualMachine/network-rules/{id}", DeletePath: "/api/VirtualMachine/network-rules/{id}",
			Fields: []restField{
				parent("vm_name", "The VM's name."),
				reqStr("name", "name", ""),
				str("rule_type", "ruleType", "inbound, outbound or route."),
				str("description", "description", ""),
				str("direction", "direction", "Inbound or Outbound."),
				num("priority", "priority", "Lower runs first (100-4096)."),
				str("protocol", "protocol", "TCP, UDP, ICMP or Any."),
				str("port_range", "portRange", "e.g. 22, 80-443 or *."),
				str("source", "source", "CIDR or *."),
				str("destination", "destination", "CIDR or *."),
				str("gateway", "gateway", "Next hop, for route rules."),
				str("action", "action", "Allow or Deny."),
				boolean("enabled", "enabled", true, ""),
			},
		},

		// ── networking ──────────────────────────────────────────────────────
		"hiok_subnet": {
			Description:  "A subnet of a virtual network.",
			CreateMethod: "PUT", CreatePath: "/api/VirtualNetwork/{vnet_id}/subnets",
			ListPath:     "/api/VirtualNetwork/{vnet_id}/subnets",
			UpdateMethod: "PUT", UpdatePath: "/api/VirtualNetwork/{vnet_id}/subnets", UpdateIDKey: "id",
			DeletePath: "/api/VirtualNetwork/{vnet_id}/subnets/{id}",
			Fields: []restField{
				parent("vnet_id", "The virtual network's id."),
				reqStr("name", "name", ""),
				str("address_space", "addressSpace", "The network's address space, e.g. 10.0.0.0/16."),
				str("size", "size", "Prefix length, e.g. /24."),
				str("ip_range", "ipRange", "The subnet's range, e.g. 10.0.1.0/24."),
				computed("vnet_name", "vnetName", fString, ""),
				computed("region_id", "regionId", fString, ""),
			},
		},
		"hiok_vnet_peering": {
			Description: "Peering between two virtual networks.",
			CreatePath:  "/api/VirtualNetwork/{vnet_id}/peerings", ListPath: "/api/VirtualNetwork/{vnet_id}/peerings",
			ListMatch: "remoteVnetName", ListMatchAttr: "remote_vnet_name", IDFormat: "{vnet_id}/{remote_vnet_name}",
			DeletePath: "/api/VirtualNetwork/{vnet_id}/peerings/{peering_id}",
			Fields: []restField{
				parent("vnet_id", "The local virtual network's id."),
				reqStr("local_vnet_name", "localVnetName", ""),
				reqStr("remote_vnet_name", "remoteVnetName", ""),
				boolean("allow_remote_to_local", "allowRemoteNetworkToAccessLocalVnet", true, ""),
				boolean("allow_local_to_remote", "allowLocalNetworkToAccessRemoteVnet", true, ""),
				boolean("enable_nat", "enableNat", false, ""),
				boolean("enable_gateway_transit", "enableGatewayTransit", false, ""),
				str("peering_type", "peeringType", "local, cross-host or cross-region."),
				computed("peering_id", "id", fString, ""),
			},
		},

		// ── storage ─────────────────────────────────────────────────────────
		"hiok_storage_container": {
			Description: "A blob container, file share or filesystem in a storage account. File shares can be " +
				"mapped as a drive (WebDAV) or opened over SFTP.",
			CreatePath: "/api/storageaccount/{account_id}/containers", IDFormat: "{account_id}/{name}",
			ReadPath:     "/api/storageaccount/{account_id}/containers/{name}",
			UpdateMethod: "PUT", UpdatePath: "/api/storageaccount/{account_id}/containers/{name}",
			DeletePath: "/api/storageaccount/{account_id}/containers/{name}?force={force_destroy}",
			Fields: []restField{
				parent("account_id", "The storage account's id."),
				reqStr("name", "name", "").forceNew(),
				str("kind", "kind", "blob (default), block, fileshare or filesystem.").forceNew(),
				str("access_level", "accessLevel", "private (default), blob or container."),
				str("access_tier", "accessTier", "hot, cool or archive."),
				boolean("versioning_enabled", "versioningEnabled", false, ""),
				num("quota_gb", "quotaGb", "For a file share: its size limit."),
				str("protocol", "protocol", "File share protocol label.").forceNew(),
				str("default_owner", "defaultOwner", "Filesystem: POSIX owner of new items.").forceNew(),
				str("default_group", "defaultGroup", "Filesystem: POSIX group of new items.").forceNew(),
				str("default_mode", "defaultMode", "Filesystem: mode of new items, e.g. 0644.").forceNew(),
				strList("replica_regions", "replicaRegions", ""),
				strMap("metadata", "metadata", ""),
				{Attr: "force_destroy", Kind: fBool, Optional: true, Default: false, PathOnly: true,
					Description: "Delete the container even if it still holds items."},
				computed("used_bytes", "usedBytes", fInt, ""),
				computed("item_count", "itemCount", fInt, ""),
				computed("mount_command", "mountCommand", fString, "For a file share: how to mount it on Linux."),
				computed("connection_info", "connection", fJSON, "For a file share: WebDAV URL, SFTP host and user name."),
			},
		},
		"hiok_storage_queue": {
			Description: "A message queue in a storage account.",
			CreatePath:  "/api/StorageAccount/{account_id}/queues", ListPath: "/api/StorageAccount/{account_id}/queues",
			ListMatch: "name", ListMatchAttr: "name", IDFormat: "{account_id}/{name}",
			DeletePath: "/api/StorageAccount/{account_id}/queues/{name}",
			Fields: []restField{
				parent("account_id", "The storage account's id."),
				reqStr("name", "name", ""),
				str("access_level", "accessLevel", ""),
			},
		},
		"hiok_storage_table": {
			Description: "A table (key/value entities) in a storage account.",
			CreatePath:  "/api/StorageAccount/{account_id}/tables", ListPath: "/api/StorageAccount/{account_id}/tables",
			ListMatch: "name", ListMatchAttr: "name", IDFormat: "{account_id}/{name}",
			DeletePath: "/api/StorageAccount/{account_id}/tables/{name}",
			Fields: []restField{
				parent("account_id", "The storage account's id."),
				reqStr("name", "name", ""),
				str("access_level", "accessLevel", ""),
			},
		},
		"hiok_storage_lifecycle_rule": {
			Description: "Moves or deletes objects automatically by age, e.g. delete backups/ after 30 days.",
			CreatePath:  "/api/StorageAccount/{account_id}/lifecycle", ListPath: "/api/StorageAccount/{account_id}/lifecycle",
			DeletePath: "/api/StorageAccount/{account_id}/lifecycle/{id}",
			Fields: []restField{
				parent("account_id", "The storage account's id."),
				reqStr("name", "name", ""),
				boolean("enabled", "enabled", true, ""),
				str("rule_type", "ruleType", "Which objects the rule looks at."),
				str("blob_prefix", "blobPrefix", "Only keys starting with this, e.g. backups/."),
				num("days_after_modification", "daysAfterModification", "Age in days before the action applies."),
				str("action", "action", "delete, cool or archive."),
			},
		},

		// ── security ────────────────────────────────────────────────────────
		"hiok_key_vault_item": {
			Description: "A secret, key or certificate in a key vault. Changing `value` stores a new version.",
			CreatePath:  "/api/KeyVault/{vault_id}/items", IDFormat: "{vault_id}/{name}",
			ReadPath:     "/api/KeyVault/{vault_id}/items/{name}",
			UpdateMethod: "POST", UpdatePath: "/api/KeyVault/{vault_id}/items",
			DeletePath: "/api/KeyVault/{vault_id}/items/{name}",
			Fields: []restField{
				parent("vault_id", "The key vault's id."),
				reqStr("name", "name", "").forceNew(),
				str("item_type", "itemType", "secret (default), key or certificate.").forceNew(),
				str("value", "value", "The secret's value. Never read back into state after creation.").sensitive(),
				str("content_type", "contentType", ""),
				boolean("enabled", "enabled", true, ""),
				str("not_before", "notBefore", "RFC 3339 time before which the item is unusable."),
				str("expires_on", "expiresOn", "RFC 3339 expiry."),
				strMap("tags", "tags", ""),
				boolean("generate", "generate", false, "Generate the value (keys and random secrets) instead of supplying one."),
				num("size", "size", "Key size or secret length when generating."),
				computed("version", "version", fString, "Current version."),
			},
		},
		"hiok_identity": {
			Description: "A managed identity, service principal or app registration. The client secret is only " +
				"available in state right after creation.",
			CreatePath: "/api/identity", ListPath: "/api/identity", DeletePath: "/api/identity/{id}",
			Fields: []restField{
				reqStr("name", "name", ""),
				reqStr("kind", "kind", "managed, serviceprincipal or appregistration."),
				computed("client_id", "clientId", fString, ""),
				computed("tenant_id", "tenantId", fString, ""),
				{Attr: "client_secret", JSON: "clientSecret", Kind: fString, Computed: true, Sensitive: true,
					Description: "Shown once, at creation."},
			},
		},
		"hiok_role_assignment": {
			Description: "Grants a user, group or identity a role on a resource.",
			CreatePath:  "/api/access-control/{resource_type}/{resource_id}/role-assignments",
			ListPath:    "/api/access-control/{resource_type}/{resource_id}/role-assignments",
			DeletePath:  "/api/access-control/{resource_type}/{resource_id}/role-assignments/{id}",
			Fields: []restField{
				parent("resource_type", "e.g. storageaccount, virtualmachine, keyvault, resourcegroup."),
				parent("resource_id", "The resource's id."),
				reqStr("principal_email", "principalEmail", "User or identity to grant the role to."),
				str("principal_name", "principalName", ""),
				str("principal_type", "principalType", "user (default), group or identity."),
				reqStr("role", "role", "Role name, e.g. Reader, Contributor, Owner (see data.hiok_roles)."),
				str("description", "description", ""),
				str("scope_type", "scopeType", ""),
				str("scope_id", "scopeId", ""),
			},
		},

		// ── governance ──────────────────────────────────────────────────────
		"hiok_resource_group": {
			Description: "A resource group: a lifecycle container for resources.",
			CreatePath:  "/api/resourcegroups", ListPath: "/api/resourcegroups", DeletePath: "/api/resourcegroups/{id}",
			Fields: []restField{
				reqStr("name", "name", ""),
				str("subscription_id", "subscriptionId", ""),
				str("location", "location", "Region, e.g. canada."),
				computed("resource_count", "resourceCount", fInt, ""),
			},
		},
		"hiok_alert_rule": {
			Description: "Emails when a measurement crosses a line, e.g. availability below 100 for 2 minutes " +
				"(the resource is down), or CPU above 90.",
			CreatePath: "/api/resource-ops/{resource_type}/{resource_id}/alerts",
			ListPath:   "/api/resource-ops/{resource_type}/{resource_id}/alerts", ListField: "rules",
			UpdateMethod: "PUT", UpdatePath: "/api/resource-ops/{resource_type}/{resource_id}/alerts/{id}",
			DeletePath: "/api/resource-ops/{resource_type}/{resource_id}/alerts/{id}",
			Fields: []restField{
				parent("resource_type", "virtualmachine, container, containerinstance, containerapp, …"),
				parent("resource_id", "The resource's id."),
				reqStr("name", "name", ""),
				str("description", "description", ""),
				reqStr("metric", "metric", "availability, cpu, memoryPercent or diskPercent."),
				str("comparison", "comparison", "gt, gte, lt or lte.").withDefault("gt"),
				restField{Attr: "threshold", JSON: "threshold", Kind: fFloat, Required: true},
				num("window_minutes", "windowMinutes", "Minutes the condition must hold.").withDefault(5),
				str("severity", "severity", "info, warning or critical.").withDefault("warning"),
				boolean("enabled", "enabled", true, ""),
				str("notify_email", "notifyEmail", "Default: the owner."),
				computed("state", "state", fString, "ok or firing."),
			},
		},
		"hiok_scheduled_task": {
			Description: "Starts, stops, restarts or snapshots a resource on a schedule — e.g. a nightly VM " +
				"snapshot keeping the newest 7.",
			CreatePath:   "/api/resource-ops/{resource_type}/{resource_id}/tasks",
			ListPath:     "/api/resource-ops/{resource_type}/{resource_id}/tasks",
			UpdateMethod: "PUT", UpdatePath: "/api/resource-ops/{resource_type}/{resource_id}/tasks/{id}",
			DeletePath: "/api/resource-ops/{resource_type}/{resource_id}/tasks/{id}",
			Fields: []restField{
				parent("resource_type", "virtualmachine or container."),
				parent("resource_id", "The resource's id."),
				reqStr("name", "name", ""),
				reqStr("action", "action", "start, stop, restart or snapshot (VMs)."),
				reqStr("schedule_time", "scheduleTime", "HH:MM, e.g. 02:30."),
				str("schedule_days", "scheduleDays", "e.g. Mon,Tue,Wed,Thu,Fri; empty = every day."),
				str("time_zone", "timeZone", "IANA time zone.").withDefault("UTC"),
				boolean("enabled", "enabled", true, ""),
				num("keep_count", "keepCount", "For snapshot: how many automatic snapshots to keep (1-30)."),
				computed("next_run", "nextRun", fString, ""),
				computed("last_result", "lastResult", fString, ""),
			},
		},
		"hiok_diagnostic_setting": {
			Description: "Where a resource's logs, metrics and traces are sent, and for how long they are kept.",
			CreatePath:  "/api/resource-ops/{resource_type}/{resource_id}/diagnostics",
			ListPath:    "/api/resource-ops/{resource_type}/{resource_id}/diagnostics", ListField: "settings",
			DeletePath: "/api/resource-ops/{resource_type}/{resource_id}/diagnostics/{id}",
			Fields: []restField{
				parent("resource_type", ""),
				parent("resource_id", ""),
				reqStr("name", "name", ""),
				str("destination", "destination", "Where telemetry goes: otel (default), prometheus, jaeger or clickhouse — whatever the region runs."),
				boolean("send_logs", "sendLogs", true, ""),
				boolean("send_metrics", "sendMetrics", true, ""),
				boolean("send_traces", "sendTraces", false, ""),
				num("retention_days", "retentionDays", "").withDefault(30),
				boolean("enabled", "enabled", true, ""),
			},
		},

		// ── integration ─────────────────────────────────────────────────────
		"hiok_servicebus_topic": {
			Description: "A publish/subscribe topic in a Service Bus namespace.",
			CreatePath:  "/api/ServiceBus/namespaces/{namespace_id}/topics", ListPath: "/api/ServiceBus/namespaces/{namespace_id}/topics",
			ListMatch: "name", ListMatchAttr: "name", IDFormat: "{namespace_id}/{name}",
			DeletePath: "/api/ServiceBus/namespaces/{namespace_id}/topics/{name}",
			Fields: []restField{
				parent("namespace_id", ""),
				reqStr("name", "name", ""),
				num("message_ttl_seconds", "messageTtlSeconds", "Default: the API's."),
				num("max_size_megabytes", "maxSizeMegabytes", "Default: the API's."),
				boolean("requires_duplicate_detection", "requiresDuplicateDetection", false, ""),
				num("duplicate_detection_window_seconds", "duplicateDetectionWindowSeconds", "Default: the API's."),
				boolean("enable_partitioning", "enablePartitioning", false, ""),
				num("partition_count", "partitionCount", "Default: the API's."),
			},
		},
		"hiok_servicebus_subscription": {
			Description: "A subscription to a Service Bus topic.",
			CreatePath:  "/api/ServiceBus/namespaces/{namespace_id}/topics/{topic}/subscriptions",
			ListPath:    "/api/ServiceBus/namespaces/{namespace_id}/topics/{topic}/subscriptions",
			ListMatch:   "name", ListMatchAttr: "name", IDFormat: "{namespace_id}/{topic}/{name}",
			DeletePath: "/api/ServiceBus/namespaces/{namespace_id}/topics/{topic}/subscriptions/{name}",
			Fields: []restField{
				parent("namespace_id", ""),
				parent("topic", "Topic name."),
				reqStr("name", "name", ""),
				num("max_delivery_count", "maxDeliveryCount", "Default: the API's."),
				num("message_ttl_seconds", "messageTtlSeconds", "Default: the API's."),
				num("lock_duration_seconds", "lockDurationSeconds", "Default: the API's."),
				boolean("requires_session", "requiresSession", false, ""),
				boolean("dead_lettering_enabled", "deadLetteringEnabled", true, ""),
				str("forward_to", "forwardTo", "Queue or topic to forward messages to."),
				str("forward_dead_lettered_to", "forwardDeadLetteredTo", ""),
			},
		},
		"hiok_servicebus_rule": {
			Description: "A filter on a topic subscription (SQL or correlation).",
			CreatePath:  "/api/ServiceBus/namespaces/{namespace_id}/topics/{topic}/subscriptions/{subscription}/rules",
			ListPath:    "/api/ServiceBus/namespaces/{namespace_id}/topics/{topic}/subscriptions/{subscription}/rules",
			ListMatch:   "name", ListMatchAttr: "name", IDFormat: "{namespace_id}/{topic}/{subscription}/{name}",
			DeletePath: "/api/ServiceBus/namespaces/{namespace_id}/topics/{topic}/subscriptions/{subscription}/rules/{name}",
			Fields: []restField{
				parent("namespace_id", ""), parent("topic", ""), parent("subscription", ""),
				reqStr("name", "name", ""),
				str("filter_type", "filterType", "sql or correlation."),
				str("sql_expression", "sqlExpression", "e.g. priority > 5."),
				jsonField("correlation", "correlation", `jsonencode({correlationId = "...", subject = "...", properties = {...}})`),
			},
		},
		"hiok_event_mesh_namespace": {
			Description: "An Event Mesh namespace holding event streams.",
			CreatePath:  "/api/Pulse/namespaces", ListPath: "/api/Pulse/namespaces", DeletePath: "/api/Pulse/namespaces/{id}",
			Fields: []restField{
				reqStr("name", "name", ""),
				str("product", "product", "Product label (the API sets it)."),
				str("sku", "sku", "").withDefault("standard"),
				str("primary_region", "primaryRegion", "").withDefault("canada"),
				strList("replica_regions", "replicaRegions", ""),
				str("broker_kind", "brokerKind", "Leave empty for the managed broker."),
				jsonField("broker_config", "brokerConfig", "Bring-your-own broker settings."),
			},
		},
		"hiok_event_consumer_group": {
			Description: "A consumer group on an event stream: its own read position.",
			CreatePath:  "/api/Pulse/namespaces/{namespace_id}/streams/{stream}/consumer-groups",
			ListPath:    "/api/Pulse/namespaces/{namespace_id}/streams/{stream}/consumer-groups",
			ListMatch:   "name", ListMatchAttr: "name", IDFormat: "{namespace_id}/{stream}/{name}",
			DeletePath: "/api/Pulse/namespaces/{namespace_id}/streams/{stream}/consumer-groups/{name}",
			Fields: []restField{
				parent("namespace_id", ""), parent("stream", ""),
				reqStr("name", "name", ""),
				str("start_from", "startFrom", "earliest or latest."),
			},
		},
		"hiok_api_management_api": {
			Description: "An API published through the API Management gateway.",
			CreatePath:  "/api/apim/apis", ListPath: "/api/apim/apis", DeletePath: "/api/apim/apis/{id}",
			Fields: []restField{
				reqStr("name", "name", ""),
				reqStr("path", "path", "Gateway path, e.g. weather."),
				reqStr("backend_url", "backendUrl", ""),
				boolean("subscription_required", "subscriptionRequired", true, ""),
			},
		},
		"hiok_api_management_operation": {
			Description: "An operation (method + URL template) of a published API.",
			CreatePath:  "/api/apim/apis/{api_id}/operations", ListPath: "/api/apim/apis/{api_id}/operations",
			UpdateMethod: "PUT", UpdatePath: "/api/apim/operations/{id}", DeletePath: "/api/apim/operations/{id}",
			Fields: []restField{
				parent("api_id", ""),
				reqStr("name", "name", ""),
				reqStr("method", "method", "GET, POST, …"),
				reqStr("url_template", "urlTemplate", "e.g. /forecast/{city}"),
				str("description", "description", ""),
				num("rate_limit_per_min", "rateLimitPerMin", ""),
				num("cache_seconds", "cacheSeconds", ""),
			},
		},
		"hiok_api_management_policy": {
			Description: "A policy (rate limit, cache, header, CORS, …) on an API or one of its operations.",
			CreatePath:  "/api/apim/apis/{api_id}/policies", ListPath: "/api/apim/apis/{api_id}/policies",
			DeletePath: "/api/apim/policies/{id}",
			Fields: []restField{
				parent("api_id", ""),
				str("operation_id", "operationId", "Limit to one operation."),
				reqStr("kind", "kind", ""),
				str("config", "config", "The policy's settings (JSON text)."),
				boolean("enabled", "enabled", true, ""),
			},
		},
		"hiok_api_management_product": {
			Description: "A product grouping APIs that subscriptions are issued for.",
			CreatePath:  "/api/apim/products", ListPath: "/api/apim/products", DeletePath: "/api/apim/products/{id}",
			Fields: []restField{
				reqStr("name", "name", ""),
				boolean("published", "published", true, ""),
				boolean("subscription_required", "subscriptionRequired", true, ""),
				strList("api_ids", "apiIds", ""),
			},
		},
		"hiok_api_management_subscription": {
			Description: "A subscription key for a product, with its rate limit.",
			CreatePath:  "/api/apim/subscriptions", ListPath: "/api/apim/subscriptions", DeletePath: "/api/apim/subscriptions/{id}",
			Fields: []restField{
				reqStr("product_id", "productId", ""),
				reqStr("name", "name", ""),
				num("rate_limit_per_min", "rateLimitPerMin", "").withDefault(60),
				{Attr: "primary_key", JSON: "primaryKey", Kind: fString, Computed: true, Sensitive: true},
			},
		},
		"hiok_hybrid_resource": {
			Description: "An on-premise server, VM, cluster or device monitored through HIOK Anywhere. The agent key " +
				"is only in state right after creation.",
			CreatePath: "/api/hybrid/resources", ListPath: "/api/hybrid/resources", DeletePath: "/api/hybrid/resources/{id}",
			Fields: []restField{
				reqStr("name", "name", ""),
				reqStr("resource_type", "resourceType", "server, vm, kubernetes, database or iotgateway."),
				str("location", "location", ""),
				{Attr: "agent_key", JSON: "agentKey", Kind: fString, Computed: true, Sensitive: true,
					Description: "Give this to the agent installer; shown once."},
				computed("status", "status", fString, ""),
			},
		},
	}

	out := map[string]*schema.Resource{}
	for name, spec := range specs {
		out[name] = spec.resource()
	}
	return out
}
