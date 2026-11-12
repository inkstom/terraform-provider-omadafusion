data "omadafusion_gateway_acls" "all" {}

output "acl_order" {
  value = [for a in data.omadafusion_gateway_acls.all.acls : "${a.index}: ${a.name} (${a.enabled ? a.policy : "disabled"})"]
}
