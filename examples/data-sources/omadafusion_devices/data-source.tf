data "omadafusion_devices" "all" {}

output "disconnected_devices" {
  value = [for d in data.omadafusion_devices.all.devices : d.name if !d.connected]
}
