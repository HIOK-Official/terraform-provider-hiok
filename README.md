# Terraform Provider for HIOK Cloud

Manage HIOK virtual machines, virtual networks, container instances, and storage accounts with Terraform.

## Status

The provider is in public preview (`0.1.x`). It has been validated with Terraform 1.15 and Go 1.26. Resource replacement is used where the HIOK API does not yet expose an in-place update operation.

## Install from source

```bash
git clone https://github.com/HIOK-Official/terraform-provider-hiok
cd terraform-provider-hiok
make install
```

## Authentication

Use a token (recommended for automation):

```bash
export HIOK_ENDPOINT="https://hiokcloud.com"
export HIOK_TOKEN="..."
```

Or let the provider obtain a token:

```bash
export HIOK_ENDPOINT="https://hiokcloud.com"
export HIOK_EMAIL="user@example.com"
export HIOK_PASSWORD="..."
```

Never commit credentials to `.tf` files.

## Example

```hcl
terraform {
  required_providers {
    hiok = {
      source  = "HIOK-Official/hiok"
      version = "~> 0.1"
    }
  }
}

provider "hiok" {
  endpoint = "https://hiokcloud.com"
  regions  = ["south-india"]
}

resource "hiok_virtual_network" "app" {
  name          = "app-net"
  address_space = "10.20.0.0/16"
}

resource "hiok_virtual_machine" "web" {
  name             = "web-01"
  image            = "ubuntu-24.04"
  vcpu_count       = 2
  ram_gb           = 4
  network_name     = hiok_virtual_network.app.name
  generate_ssh_key = true
}
```

A complete configuration is in [`examples/main.tf`](examples/main.tf).

## Resources

- `hiok_virtual_machine`
- `hiok_virtual_network`
- `hiok_container`
- `hiok_storage_account`

## Data sources

- `hiok_regions`
- `hiok_vm_images`
- `hiok_virtual_machine`

## Development

```bash
make fmt
make vet
make test
make build
```

Then run:

```bash
cd examples
terraform init -backend=false
terraform validate
```

## Security

Report vulnerabilities privately to `security@hiokcloud.com`. Do not open a public issue for a credential or access-control problem.
