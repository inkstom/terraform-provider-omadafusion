data "omadafusion_wan_ports" "this" {}

# Remote-access VPN on the gateway. The controller generates the key pair;
# give public_key to clients.
resource "omadafusion_wireguard" "remote" {
  name        = "remote-access"
  listen_port = 51820
  local_ip    = "10.250.0.1"
}

output "wireguard_public_key" {
  value = omadafusion_wireguard.remote.public_key
}
