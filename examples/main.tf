terraform {
  required_providers {
    hiok = {
      source  = "HIOK-Official/hiok"
      version = "~> 0.1"
    }
  }
}

# Credentials come from HIOK_ENDPOINT / HIOK_EMAIL / HIOK_PASSWORD (or HIOK_TOKEN)
# so nothing sensitive has to live in the configuration.
provider "hiok" {
  endpoint = "https://hiokcloud.com"
  regions  = ["south-india"]
}

data "hiok_regions" "available" {}

data "hiok_vm_images" "ubuntu" {}

resource "hiok_virtual_network" "app" {
  name          = "app-net"
  address_space = "10.20.0.0/16"
  subnet_name   = "web"
  subnet_cidr   = "10.20.1.0/24"
}

resource "hiok_virtual_machine" "web" {
  name             = "web-01"
  region           = "south-india"
  image            = "ubuntu-24.04"
  vcpu_count       = 2
  ram_gb           = 4
  network_name     = hiok_virtual_network.app.name
  username         = "ubuntu"
  generate_ssh_key = true
}

resource "hiok_container" "api" {
  name  = "api-01"
  image = "nginx:alpine"
  env   = ["NODE_ENV=production"]
}

resource "hiok_storage_account" "assets" {
  name       = "assets"
  tier       = "standard"
  redundancy = "lrs"
}

output "vm_private_ip" {
  value = hiok_virtual_machine.web.private_ip
}

output "regions" {
  value = data.hiok_regions.available.ids
}
