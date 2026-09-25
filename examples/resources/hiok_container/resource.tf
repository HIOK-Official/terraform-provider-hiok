resource "hiok_container" "example" {
  name    = "api-01"
  image   = "nginx:alpine"
  env     = ["NODE_ENV=production"]
  command = ["nginx", "-g", "daemon off;"]
}
