# Changelog

## 0.1.0 (unreleased)

First release, verified end to end against the HIOK test deployment.

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
