resource "hiok_virtual_machine" "example" {
  name             = "web-01"
  region           = "south-india"
  image            = "ubuntu-24.04"
  vcpu_count       = 2
  ram_gb           = 4
  network_name     = "app-net"
  username         = "ubuntu"
  generate_ssh_key = true

  timeouts {
    create = "30m"
    delete = "20m"
  }
}
