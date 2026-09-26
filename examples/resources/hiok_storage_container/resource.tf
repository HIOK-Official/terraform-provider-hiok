# A file share, mappable as a drive (WebDAV) or reachable over SFTP.
resource "hiok_storage_container" "team" {
  account_id = hiok_storage_account.example.id
  name       = "team"
  kind       = "fileshare"
  quota_gb   = 50
}

output "webdav_url" { value = jsondecode(hiok_storage_container.team.connection_info).webDavUrl }
