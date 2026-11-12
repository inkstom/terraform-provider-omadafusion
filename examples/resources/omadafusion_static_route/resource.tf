data "omadafusion_lan_network" "management" { name = "Management" }

# Reach a lab network behind a router on the management LAN.
resource "omadafusion_static_route" "lab" {
  name         = "Lab"
  destinations = ["10.99.0.0/16"]
  next_hop     = "192.168.10.254"
  interface_id = data.omadafusion_lan_network.management.id
}
