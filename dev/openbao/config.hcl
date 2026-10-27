ui            = false
disable_mlock = true
api_addr      = "http://127.0.0.1:8200"
cluster_addr  = "http://127.0.0.1:8201"

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

storage "raft" {
  path    = "/openbao/file"
  node_id = "openbao"
}

seal "static" {
  current_key_id = "dev"
  current_key    = "env://BAO_SEAL_KEY"
}
