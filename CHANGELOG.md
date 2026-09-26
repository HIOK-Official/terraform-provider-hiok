# Changelog

## 0.3.0

- Service principals: `client_id` and `client_secret` (or `HIOK_CLIENT_ID` /
  `HIOK_CLIENT_SECRET`) sign the provider in without anyone's password — the
  credential for CI/CD. The one-hour token is renewed automatically.

## 0.2.0

The whole platform as code: 60 resources and 12 data sources, verified end to end
against the HIOK test deployment (apply, empty re-plan, in-place update, import, destroy).

### Breaking
- `hiok_storage_account`: the id is now the account's GUID (it was the name), which is
  what every child resource and the API take. Existing state is upgraded automatically;
  `account_id` stays and equals `id`. Import accepts the name or the GUID.

### New resources
- Storage: `hiok_storage_container` (blob containers and file shares, with WebDAV/SFTP
  `connection_info`), `hiok_storage_queue`, `hiok_storage_table`, `hiok_storage_lifecycle_rule`.
- Compute: `hiok_container_job` (cron or on demand, Key Vault secrets, storage mounts),
  `hiok_container_app`, `hiok_container_app_environment`, `hiok_container_registry`,
  `hiok_kubernetes_cluster`, `hiok_docker_stack`, `hiok_swarm_service`, `hiok_vm_snapshot`.
- Networking: `hiok_public_ip`, `hiok_vpn_gateway`, `hiok_vpn_client`, `hiok_bastion`,
  `hiok_vnet_peering`, `hiok_vm_network_rule`.
- Data: `hiok_postgres_database`, `hiok_mysql_database`, `hiok_sqlserver_database`,
  `hiok_mongo_cluster`, `hiok_yugabyte_cluster`, `hiok_cache`, `hiok_analytics_cluster`.
- Messaging and streaming: Service Bus namespaces, queues, topics, subscriptions and rules;
  Event Mesh namespaces, streams and consumer groups; stream jobs and destinations.
- Security and governance: `hiok_key_vault`, `hiok_key_vault_item`, `hiok_identity`,
  role assignments, `hiok_resource_group`, alert rules, scheduled tasks, diagnostic settings.
- API Management APIs, operations, products and subscriptions; communication services;
  IoT devices and DPS enrollments; hybrid resources.

See the docs folder for the full list and every argument.

### New data sources
`hiok_storage_account_keys`, `hiok_key_vault_secret`, `hiok_container_registry_credentials`,
`hiok_roles`, `hiok_resource_groups`, `hiok_ip_pools`.

### Improvements
- `hiok_container`: `entrypoint`, `labels`, `ports`, `volumes`, `restart_policy`, `networks`,
  `network_mode`, `memory_bytes`, `cpus`, `user`, `working_dir`, `stop_signal`, `domainname`, `tty`.
- `hiok_virtual_machine`: `auth_type`, `mac_address`, static `subnet_ip`/`subnet_mask`/`gateway`,
  OVS bridge options. `hiok_virtual_network`: `forward_mode`, `bridge_name`.
- Options the API fills in when left out no longer show as changes on the next plan.
- A resource is dropped from state only when the API says it is gone (404), never on a
  transient error.
- `hiok_storage_account`: `replica_regions`, `consistency_mode`, `write_acknowledgement`,
  `max_staleness_ms`, `quota_bytes`, `description`, resource group and subscription placement.

## 0.1.0

Not released separately; included in 0.2.0.

First version, verified end to end against the HIOK test deployment.

### Resources
- `hiok_virtual_machine`: create, import, destroy; `power_state` (start/stop in place); locally generated SSH keys (`private_key_openssh`); `private_ip`, `public_ip`, `hostname`, `ssh_command`; `disk_size_gb`, password authentication.
- `hiok_virtual_network`: create with a named subnet, change the subnet in place, import, destroy.
- `hiok_container`: create, import, destroy; `dns_hostname`.
- `hiok_storage_account`: create, import, destroy; `tier`, `redundancy`, `display_name` change in place.

### Data sources
`hiok_regions` (with `available_ids`), `hiok_vm_images`, `hiok_virtual_machine`, `hiok_virtual_network`, `hiok_container`, `hiok_storage_account`.

### Behaviour
- Creates wait until the API lists the resource; destroys wait until it is gone.
- Unknown or unavailable regions are rejected immediately with the list of valid regions.
- API errors are reported with the platform's own message.
- Temporary API outages are retried and waited out.
- Importing never forces a replacement.
