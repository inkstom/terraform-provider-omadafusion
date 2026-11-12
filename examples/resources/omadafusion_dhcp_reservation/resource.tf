data "omadafusion_lan_network" "devices" {
  name = "Devices"
}

resource "omadafusion_dhcp_reservation" "printer" {
  mac         = "aa:bb:cc:11:22:33"
  ip          = "192.168.30.10"
  network_id  = data.omadafusion_lan_network.devices.id
  description = "Front desk printer"
}
