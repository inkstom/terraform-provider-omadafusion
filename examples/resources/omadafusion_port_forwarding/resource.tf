data "omadafusion_wan_ports" "this" {}

locals {
  wan1 = one([for p in data.omadafusion_wan_ports.this.ports : p.id if p.name == "WAN1"])
}

# Forward WireGuard to an internal VPN host, only from one partner network.
resource "omadafusion_port_forwarding" "wireguard" {
  name          = "WireGuard"
  wan_port_ids  = [local.wan1]
  source_cidrs  = ["198.51.100.0/24"]
  external_port = "51820"
  forward_ip    = "192.168.10.20"
  forward_port  = "51820"
  protocol      = "udp"
}
