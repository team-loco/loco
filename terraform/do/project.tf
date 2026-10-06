resource "digitalocean_project" "loco" {
  name        = "loco-project"
  description = "DOKS"
  purpose     = "Web Application"
  environment = "Development"
  resources   = [digitalocean_kubernetes_cluster.loco.urn]
}
