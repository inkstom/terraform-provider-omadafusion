data "omadafusion_lan_network" "staff" {
  name = "Staff"
}

resource "omadafusion_lan_network_dhcp_dns" "staff" {
  network_id    = data.omadafusion_lan_network.staff.id
  primary_dns   = "1.1.1.1"
  secondary_dns = "1.0.0.1"
}
