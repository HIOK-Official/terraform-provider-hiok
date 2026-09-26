# The identity a pipeline deploys as, limited to the runner's addresses.
resource "hiok_service_principal" "github" {
  name          = "github-deploy"
  role          = "contributor"
  allowed_cidrs = ["203.0.113.0/24"]
}

output "HIOK_CLIENT_ID" { value = hiok_service_principal.github.client_id }
output "HIOK_CLIENT_SECRET" {
  value     = hiok_service_principal.github.client_secret
  sensitive = true
}
