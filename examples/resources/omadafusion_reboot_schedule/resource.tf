data "omadafusion_devices" "all" {}

# Reboot every AP early on the first of the month.
resource "omadafusion_reboot_schedule" "aps" {
  name         = "Monthly AP reboot"
  device_macs  = [for d in data.omadafusion_devices.all.devices : d.mac if d.type == "ap"]
  schedule     = "monthly"
  day_of_month = 1
  hour         = 4
}
