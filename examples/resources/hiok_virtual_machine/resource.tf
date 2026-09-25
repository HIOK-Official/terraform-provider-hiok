resource "hiok_virtual_machine" "example" {
  name             = "web-01"
  region           = "canada"
  image            = "ubuntu-24.04-amd64"
  vcpu_count       = 2
  ram_gb           = 4
  network_name     = "app-net"
  username         = "ubuntu"
  generate_ssh_key = true
  power_state      = "running"

  timeouts {
    create = "30m"
    delete = "20m"
  }
}
