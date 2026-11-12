data "omadafusion_lan_network" "devices" { name = "Devices" }
data "omadafusion_lan_network" "voice" { name = "Voice" }

# Access port for printers and other fixed devices, with a tagged voice VLAN
# for a desk phone pass-through.
resource "omadafusion_port_profile" "devices_access" {
  name               = "Devices access"
  native_network_id  = data.omadafusion_lan_network.devices.id
  tagged_network_ids = [data.omadafusion_lan_network.voice.id]
  loopback_detect    = true
}
