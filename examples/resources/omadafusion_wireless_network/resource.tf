variable "staff_wifi_passphrase" {
  type      = string
  sensitive = true
}

data "omadafusion_lan_network" "staff" { name = "Staff" }

resource "omadafusion_wireless_network" "staff" {
  name         = "Clinic Staff"
  passphrase   = var.staff_wifi_passphrase
  network_id   = data.omadafusion_lan_network.staff.id
  bands        = ["2.4ghz", "5ghz"]
  fast_roaming = true
}
