terraform {
  required_version = ">= 1.5"
  required_providers {
    hiok = {
      source  = "HIOK-Official/hiok"
      version = "~> 0.1"
    }
  }
}

# Endpoint and credentials come from the environment so nothing sensitive
# (and no environment-specific URL) lives in the configuration:
#   export HIOK_ENDPOINT="https://test.hiokcloud.com"
#   export HIOK_EMAIL="you@example.com" HIOK_PASSWORD="..."   # or HIOK_TOKEN
provider "hiok" {
  regions = [var.region]
}

variable "region" {
  type        = string
  default     = "canada"
  description = "See data.hiok_regions.available.available_ids for the regions accepting new resources."
}

variable "prefix" {
  type        = string
  default     = "demo"
  description = "Prefix for every resource name, so several people can share one account."
}

data "hiok_regions" "available" {}

data "hiok_vm_images" "available" {}

resource "hiok_virtual_network" "app" {
  name          = "${var.prefix}-app-net"
  address_space = "10.20.0.0/16"
  subnet_name   = "web"
  subnet_cidr   = "10.20.1.0/24"
}

resource "hiok_virtual_machine" "web" {
  name             = "${var.prefix}-web-01"
  image            = "ubuntu-24.04-amd64"
  vcpu_count       = 1
  ram_gb           = 1
  disk_size_gb     = 10
  network_name     = hiok_virtual_network.app.name
  username         = "ubuntu"
  generate_ssh_key = true

  timeouts {
    create = "30m"
  }
}

resource "hiok_container" "api" {
  name  = "${var.prefix}-api-01"
  image = "nginx:alpine"
  env   = ["NODE_ENV=production"]
}

resource "hiok_storage_account" "assets" {
  name       = "${var.prefix}assets"
  tier       = "hot"
  redundancy = "LRS"
}

output "vm_hostname" {
  value = hiok_virtual_machine.web.hostname
}

output "container_hostname" {
  value = hiok_container.api.dns_hostname
}

output "vm_status" {
  value = hiok_virtual_machine.web.status
}

output "available_regions" {
  value = data.hiok_regions.available.available_ids
}

output "images" {
  value = data.hiok_vm_images.available.ids
}
