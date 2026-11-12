data "omadafusion_controller" "this" {}

output "controller_version" {
  value = data.omadafusion_controller.this.controller_version
}
