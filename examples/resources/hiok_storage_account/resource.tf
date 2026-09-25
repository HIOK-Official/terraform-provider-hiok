resource "hiok_storage_account" "example" {
  name       = "assets"
  tier       = "standard"
  redundancy = "lrs"
}
