resource "omadafusion_group" "printers" {
  name    = "Printers"
  type    = "ip"
  subnets = [{ cidr = "192.168.30.10/32", description = "front desk" }]
}

resource "omadafusion_group" "admin_ports" {
  name    = "Admin ports"
  type    = "ip_port"
  subnets = [{ cidr = "192.168.10.0/24" }]
  ports   = ["22", "443", "8043"]
}
