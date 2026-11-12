data "omadafusion_lan_network" "staff" {
  name = "Staff"
}

data "omadafusion_lan_network" "management" {
  name = "Management"
}

# Staff cannot open connections to Management, but replies to connections
# that Management opens still pass.
resource "omadafusion_gateway_acl" "staff_to_management" {
  name            = "Deny Staff to Management"
  policy          = "deny"
  protocols       = ["all"]
  source_ids      = [data.omadafusion_lan_network.staff.id]
  destination_ids = [data.omadafusion_lan_network.management.id]
  match_states    = ["new"]
}

# Block Staff from the gateway's admin pages. destination_type 5 matches all
# traffic addressed to the gateway, so limit it to TCP to keep DNS/DHCP/ping.
resource "omadafusion_gateway_acl" "staff_to_gateway_admin" {
  name             = "Deny Staff to gateway management"
  policy           = "deny"
  protocols        = ["tcp"]
  source_ids       = [data.omadafusion_lan_network.staff.id]
  destination_type = 5
}
