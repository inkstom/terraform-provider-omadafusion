data "omadafusion_ssid" "guest" {
  name = "Guest WiFi"
}

# Guest Wi-Fi only broadcasts during business hours.
resource "omadafusion_ssid_schedule" "guest" {
  ssid_id       = data.omadafusion_ssid.guest.id
  time_range_id = omadafusion_time_range.business_hours.id
  action        = "radio_on"
}
