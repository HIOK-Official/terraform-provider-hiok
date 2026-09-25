resource "hiok_storage_account" "example" {
  name       = "assets"
  tier       = "hot"
  redundancy = "LRS"
}
