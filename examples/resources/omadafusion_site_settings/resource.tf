# Only the attributes set here are managed.
resource "omadafusion_site_settings" "this" {
  timezone             = "America/New_York"
  ntp_servers          = ["time.cloudflare.com", "pool.ntp.org"]
  auto_upgrade_enabled = false # upgrade in a maintenance window instead
  led_enabled          = true
  lldp_enabled         = true
  syslog_enabled       = true
  syslog_host          = "192.168.10.20"
  syslog_port          = 514
  device_ssh_enabled   = false
  upnp_enabled         = false
}
