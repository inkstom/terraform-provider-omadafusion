terraform {
  required_providers {
    omadafusion = {
      source = "inkstom/omadafusion"
    }
  }
}

# A small clinic or office: Management, Staff, Devices and Guest networks
# already exist on the gateway. This config adds segmentation, public DNS for
# Staff, fixed addresses for infrastructure, and guest Wi-Fi that is only on
# during business hours.

provider "omadafusion" {
  site     = "Default"
  insecure = true
}

data "omadafusion_lan_network" "management" { name = "Management" }
data "omadafusion_lan_network" "staff" { name = "Staff" }
data "omadafusion_lan_network" "devices" { name = "Devices" }
data "omadafusion_ssid" "guest" { name = "Guest WiFi" }

# --- Segmentation -----------------------------------------------------------

locals {
  one_way_denies = {
    "Deny Staff to Management"   = [data.omadafusion_lan_network.staff.id, data.omadafusion_lan_network.management.id]
    "Deny Devices to Management" = [data.omadafusion_lan_network.devices.id, data.omadafusion_lan_network.management.id]
    "Deny Devices to Staff"      = [data.omadafusion_lan_network.devices.id, data.omadafusion_lan_network.staff.id]
  }
}

resource "omadafusion_gateway_acl" "one_way" {
  for_each = local.one_way_denies

  name            = each.key
  policy          = "deny"
  source_ids      = [each.value[0]]
  destination_ids = [each.value[1]]
  match_states    = ["new"] # replies to connections opened from the other side still pass
}

resource "omadafusion_gateway_acl" "no_gateway_admin" {
  name             = "Deny Staff and Devices to gateway management"
  policy           = "deny"
  protocols        = ["tcp"] # "all" would also block DNS, DHCP and ping to the gateway
  source_ids       = [data.omadafusion_lan_network.staff.id, data.omadafusion_lan_network.devices.id]
  destination_type = 5

  depends_on = [omadafusion_gateway_acl.one_way]
}

# Blocking TCP to the gateway also blocks DNS-over-TCP to it, so hand these
# networks public resolvers instead.
resource "omadafusion_lan_network_dhcp_dns" "public" {
  for_each = {
    staff   = data.omadafusion_lan_network.staff.id
    devices = data.omadafusion_lan_network.devices.id
  }

  network_id    = each.value
  primary_dns   = "1.1.1.1"
  secondary_dns = "1.0.0.1"
}

# --- Fixed addresses ----------------------------------------------------------

variable "infrastructure" {
  description = "Management-network reservations, keyed by device name."
  type = map(object({
    mac = string
    ip  = string
  }))
  default = {
    "CORE-SW" = { mac = "AA-BB-CC-00-00-02", ip = "192.168.10.2" }
    "AP-01"   = { mac = "AA-BB-CC-00-00-04", ip = "192.168.10.4" }
  }
}

resource "omadafusion_dhcp_reservation" "infrastructure" {
  for_each = var.infrastructure

  mac         = each.value.mac
  ip          = each.value.ip
  network_id  = data.omadafusion_lan_network.management.id
  description = each.key
}

# --- Guest Wi-Fi after hours --------------------------------------------------

resource "omadafusion_time_range" "business_hours" {
  name    = "Business hours"
  days    = ["mon", "tue", "wed", "thu", "fri"]
  periods = [{ start = "07:30", end = "18:00" }]
}

resource "omadafusion_ssid_schedule" "guest" {
  ssid_id       = data.omadafusion_ssid.guest.id
  time_range_id = omadafusion_time_range.business_hours.id
}
