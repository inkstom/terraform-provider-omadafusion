data "omadafusion_wan_ports" "this" {}

output "wan_ports" {
  value = { for p in data.omadafusion_wan_ports.this.ports : p.name => p.id }
}
