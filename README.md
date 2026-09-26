# Terraform Provider for HIOK Cloud

Manage HIOK virtual machines, virtual networks, container instances, and storage accounts with Terraform.

## Status

The provider is published on the Terraform Registry as `HIOK-Official/hiok` and needs Terraform 1.5 or later. It is tested end to end against the live HIOK deployment.

Changed in place (no replacement): a VM's `power_state`, a storage account's `tier`, `redundancy` and `display_name`, and a network's `subnet_name`/`subnet_cidr`. The HIOK API has no update operation for anything else, so **changing any other setting replaces the resource**. For a virtual machine that means its disk is deleted too. Read the plan before you apply, and consider `lifecycle { prevent_destroy = true }` on anything that holds data.

## Install

From the Terraform Registry (once a release is published):

```hcl
terraform {
  required_providers {
    hiok = {
      source  = "HIOK-Official/hiok"
      version = "~> 0.1"
    }
  }
}
```

From source (Linux, macOS, Windows; any architecture):

```bash
git clone https://github.com/HIOK-Official/terraform-provider-hiok
cd terraform-provider-hiok
make install
```

## Authentication

The endpoint is required. Use a token (recommended for automation):

```bash
export HIOK_ENDPOINT="https://hiokcloud.com"      # or https://hiokcloud.com
export HIOK_TOKEN="..."
```

Or let the provider sign in (it also re-signs in automatically if the token expires during a long apply):

```bash
export HIOK_ENDPOINT="https://hiokcloud.com"
export HIOK_EMAIL="user@example.com"
export HIOK_PASSWORD="..."
```

Never commit credentials to `.tf` files. A value set in the `provider` block overrides the environment variable, so leave `endpoint` out of shared configurations.

## Example

```hcl
provider "hiok" {
  regions = ["canada"]
}

resource "hiok_virtual_network" "app" {
  name          = "app-net"
  address_space = "10.20.0.0/16"
}

resource "hiok_virtual_machine" "web" {
  name             = "web-01"
  image            = "ubuntu-24.04-amd64"
  vcpu_count       = 1
  ram_gb           = 1
  network_name     = hiok_virtual_network.app.name
  generate_ssh_key = true
}
```

A complete configuration is in [`examples/main.tf`](examples/main.tf). Full reference: [`docs/`](docs/index.md).

## Resources

| Resource | Import ID |
|---|---|
| `hiok_virtual_machine` | name |
| `hiok_virtual_network` | name |
| `hiok_container` | name |
| `hiok_storage_account` | name |

Values accepted by the live API (checked against hiokcloud.com):

- `region`: IDs from `data.hiok_regions.x.available_ids` (currently `canada`). When unset, the provider uses the first available region.
- `image`: IDs from `data.hiok_vm_images.x.ids`, e.g. `ubuntu-24.04-amd64`, `ubuntu-22.04-amd64`, `debian-12-amd64`.
- storage `tier`: `hot`, `cool`, `cold`, `archive`; `redundancy`: `LRS`, `ZRS`, `GRS`, `RA-GRS`.

All resources:

- wait until the API actually lists them after create, and until they are gone after destroy (configurable with a `timeouts` block);
- refuse to create over an existing name and suggest `terraform import` instead;
- can be imported without being replaced: settings the API does not report back are recorded on the next apply (shown as an in-place update, no API call);
- ride out short API outages (retries, and waits that keep polling through 502/503/504/52x responses).

Virtual machines also expose `private_ip`, `public_ip`, `hostname` and `ssh_command`. With `generate_ssh_key = true` the provider generates an ed25519 keypair itself (the platform's own key generation does not keep a retrievable key) and returns the private key in the sensitive `private_key_openssh` attribute, which is stored in Terraform state. Protect your state accordingly, or pass your own `ssh_public_key`.

## Data sources

- `hiok_regions`
- `hiok_vm_images`
- `hiok_virtual_machine`
- `hiok_virtual_network`
- `hiok_container`
- `hiok_storage_account`

## Development

```bash
make fmt
make vet
make test     # real Terraform plan/apply/import/destroy against an in-memory mock API
make build
```

Try the provider without an account:

```bash
make install
make demo                                   # mock API on 127.0.0.1:18080 (leave running)
export HIOK_ENDPOINT=http://127.0.0.1:18080 HIOK_TOKEN=dev
cd examples && terraform init && terraform apply && terraform destroy
```

Acceptance tests against a real deployment (creates and then destroys real, billable resources named `tf-acc-*`):

```bash
export HIOK_ENDPOINT=https://hiokcloud.com HIOK_EMAIL=... HIOK_PASSWORD=...
make testacc
```

## Releasing

Push a `vX.Y.Z` tag. The release workflow builds and signs the artifacts the Terraform Registry expects. It needs the `GPG_PRIVATE_KEY` and `PASSPHRASE` repository secrets, and the matching public key must be added to the Registry namespace.

## Security

Report vulnerabilities privately to `security@hiokcloud.com`. Do not open a public issue for a credential or access-control problem.
