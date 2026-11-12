data "omadafusion_wan_ports" "this" {}

variable "noip_password" {
  type      = string
  sensitive = true
}

resource "omadafusion_ddns" "office" {
  service     = "noip"
  wan_port_id = data.omadafusion_wan_ports.this.ports[0].id
  username    = "office-it@example.org"
  password    = var.noip_password
  domain      = "office-example.ddns.net"
}
