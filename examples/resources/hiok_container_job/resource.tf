# A batch program run every 15 minutes, with a Backblaze key from Key Vault and
# its state on a storage volume.
resource "hiok_container_job" "b2_sync" {
  name      = "b2-sync"
  image     = "myregistry-canada-abc123.hiokcloud.com:5000/batch-b2:1.0"
  schedule  = "*/15 * * * *"
  time_zone = "Asia/Kolkata"

  environment = { BATCH_APP = "FILTRAN~~BACKBLAZE" }
  secrets = jsonencode([
    { env = "B2_KEYID", vaultId = hiok_key_vault.app.id, secret = "b2-keyid" },
    { env = "B2_APPKEY", vaultId = hiok_key_vault.app.id, secret = "b2-appkey" },
  ])
  volumes = jsonencode([
    { type = "storage", source = "${hiok_storage_account.example.name}/batch", destination = "/data", readOnly = false },
  ])

  cpus            = 0.5
  memory_mb       = 256
  timeout_minutes = 30
  max_retries     = 1
}
