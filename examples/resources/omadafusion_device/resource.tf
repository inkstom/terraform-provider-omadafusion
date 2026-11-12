# Name an adopted AP, turn its LED off and pin its 5 GHz channel.
resource "omadafusion_device" "lobby_ap" {
  mac  = "aa:bb:cc:00:00:02"
  name = "AP-Lobby"
  led  = "off"
  radio_5g = {
    channel        = "36"
    tx_power_level = 2
  }
}
