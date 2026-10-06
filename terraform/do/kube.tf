resource "digitalocean_kubernetes_cluster" "loco" {
  version = "1.32.2-do.3"

  name   = "loco-cluster"
  region = "nyc1"


  node_pool {
    name = "worker-pool"
    size = "s-2vcpu-2gb"

    auto_scale = true
    min_nodes  = 1
    max_nodes  = 3
  }
}
