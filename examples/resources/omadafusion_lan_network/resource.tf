data "omadafusion_lan_network" "management" { name = "Management" }

# A new VLAN for cameras with its own DHCP pool and public DNS.
resource "omadafusion_lan_network" "cameras" {
  name             = "Cameras"
  vlan             = 60
  gateway_subnet   = "192.168.60.1/24"
  dhcp_start       = "192.168.60.100"
  dhcp_end         = "192.168.60.199"
  dhcp_dns_servers = ["1.1.1.1", "1.0.0.1"]
}

# An internet-only guest network (gateway-enforced isolation).
resource "omadafusion_lan_network" "guest" {
  name           = "Guest"
  vlan           = 40
  gateway_subnet = "192.168.40.1/24"
  isolation      = true
}
