# Evaluate the narrow permit before the broad deny.
resource "omadafusion_gateway_acl_order" "this" {
  acl_ids = [
    omadafusion_gateway_acl.printer_scan_permit.id,
    omadafusion_gateway_acl.devices_to_staff_deny.id,
  ]
}
