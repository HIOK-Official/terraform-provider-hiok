resource "hiok_key_vault" "app" { name = "app-secrets" }

resource "hiok_key_vault_item" "b2_appkey" {
  vault_id = hiok_key_vault.app.id
  name     = "b2-appkey"
  value    = var.b2_appkey # never commit the value itself
}
