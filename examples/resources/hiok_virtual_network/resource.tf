resource "hiok_virtual_network" "example" {
  name          = "app-net"
  address_space = "10.20.0.0/16"
  subnet_name   = "web"
  subnet_cidr   = "10.20.1.0/24"
}
