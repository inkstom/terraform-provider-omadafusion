variable "helpdesk_password" {
  type      = string
  sensitive = true
}

# A read-only account for an outside helper, limited to one site.
resource "omadafusion_admin_user" "helpdesk" {
  name     = "helpdesk"
  password = var.helpdesk_password
  role     = "viewer"
  sites    = ["Default"]
}
