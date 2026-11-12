# Build a jack map: one entry per wall jack.
locals {
  jacks = {
    "jack-12 front desk printer" = { switch = "AA-BB-CC-00-00-02", port = 9 }
    "jack-14 exam room 2"        = { switch = "AA-BB-CC-00-00-02", port = 10 }
  }
}

resource "omadafusion_switch_port" "jack" {
  for_each   = local.jacks
  switch_mac = each.value.switch
  port       = each.value.port
  name       = each.key
  profile_id = omadafusion_port_profile.devices_access.id
}
